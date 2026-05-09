// Bubbletea-driven interactive TUI. Replaces the bufio.Scanner blocking loop
// when stdin is a TTY and --print is not set, so that input keystrokes are
// accepted concurrently with the thinking spinner. Headless paths (--print
// and piped stdin) keep using runScannerLoop in main.go.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// tuiOptions bundles inputs runTUI needs from main().
type tuiOptions struct {
	name           string
	sessionID      string
	resumed        bool
	cwd            string
	transcriptPath string
	permissionMode string
	delay          time.Duration
	exitAfter      int
	autoExit       time.Duration
	historyCap     int // 0 = unlimited
	statusLine     string
	settings       *Settings
	mcpConfig      *MCPConfig
	hooks          *HookSender
	mcp            *MCPClient
	slash          *SlashHandler
}

// model is the bubbletea Model driving the interactive session.
type model struct {
	opts tuiOptions

	history []string // rendered scrollback
	pending []string // queued prompts submitted while thinking

	input textinput.Model
	spin  spinner.Model

	thinking      bool
	thinkingInput string // current prompt being processed
	thinkStart    time.Time
	thinkTag      int // increments on each new thinking turn so stale ticks are ignored

	width, height int

	count      int
	quitReason string
	quitCode   int
	bannerDone bool
}

// thinkingDoneMsg fires when the simulated thinking delay elapses for tag tag.
type thinkingDoneMsg struct {
	tag      int
	response string
}

// slashDoneMsg fires when an asynchronously-dispatched slash command finishes.
type slashDoneMsg struct {
	rendered string
	outcome  SlashOutcome
}

// hookErrMsg surfaces a hook error from a goroutine. When err is nil, no-op.
type hookErrMsg struct {
	stage string
	err   error
}

// mcpConnectMsg fires after the initial best-effort MCP connect attempt
// and the boot SessionStart. Both run in the same goroutine (cmdBoot) so
// SessionStart fires synchronously after mcp.Connect returns — that way
// SessionStart lands on the wire even if the user quits before this message
// is delivered to Update. Note: this serializes Connect→SessionStart within
// the boot goroutine but does NOT serialize against user-driven hook cmds
// (e.g. /restart) running on their own goroutines; in practice mcp.Connect
// is fast enough that no realistic user input beats it, but a paranoid
// orchestrator should not depend on strict ordering between boot
// SessionStart and the very first user-submitted hook.
type mcpConnectMsg struct {
	err      error
	tools    int
	startErr error
}

// autoExitMsg fires when --auto-exit elapses.
type autoExitMsg struct{}

// cancelMsg fires when the user presses Esc during a thinking turn.
type cancelMsg struct{}

// styles
var (
	tuiStylePrompt  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	tuiStyleEcho    = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	tuiStyleDim     = lipgloss.NewStyle().Faint(true)
	tuiStyleSpin    = lipgloss.NewStyle().Faint(true)
	tuiStyleThought = lipgloss.NewStyle().Faint(true).Italic(true)
)

// newModel builds the initial model. The textinput and spinner are
// bubbles components; both honor m.width on each Update.
func newModel(opts tuiOptions) model {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = tuiStylePrompt.Render("> ")
	ti.Focus()
	ti.CharLimit = 0

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = tuiStyleSpin

	return model{
		opts:  opts,
		input: ti,
		spin:  sp,
	}
}

// banner renders the rounded banner shown once at session start. Same shape
// as the scanner-path banner so users see the same intro across both modes.
func banner(opts tuiOptions) string {
	sessionLabel := "session"
	if opts.resumed {
		sessionLabel = "resumed"
	}
	content := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render(opts.name),
		lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Faint(true).Render(sessionLabel+" "+opts.sessionID),
		lipgloss.NewStyle().Faint(true).Render("Type anything; /help for commands"),
	)
	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("14")).
		Padding(0, 2).
		Render(content)
}

// Init seeds the initial command batch: spinner ticks (so it animates when
// thinking starts), textinput cursor blink, the boot sequence (cmdBoot does
// MCP connect → SessionStart in one goroutine), and optional auto-exit
// timer. The banner and status line are appended to history here so they
// appear once on first render. Coupling MCP connect and SessionStart in one
// cmd means SessionStart fires regardless of whether the model is still
// alive to process the resulting message — see the note on mcpConnectMsg
// for the serialization caveat against concurrent user-driven hook cmds.
func (m model) Init() tea.Cmd {
	startSource := "startup"
	if m.opts.resumed {
		startSource = "resume"
	}
	cmds := []tea.Cmd{
		textinput.Blink,
		m.spin.Tick,
		cmdBoot(m.opts.mcp, m.opts.hooks, startSource),
	}
	if m.opts.autoExit > 0 {
		cmds = append(cmds, cmdAutoExit(m.opts.autoExit))
	}
	return tea.Batch(cmds...)
}

// Update is the model's event handler. Bubbletea serializes Update calls so
// no mutex is needed; long-running work is pushed onto goroutines via tea.Cmd.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Lazy banner injection — Init can't mutate state, so we do it once on
	// the first Update tick.
	if !m.bannerDone {
		m.history = append(m.history, banner(m.opts))
		if m.opts.statusLine != "" {
			m.history = append(m.history, tuiStyleDim.Render("["+m.opts.statusLine+"]"))
		}
		m.bannerDone = true
	}

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		w := msg.Width - 2
		if w < 1 {
			w = 1
		}
		m.input.Width = w

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyCtrlD:
			m.quitReason = "other"
			return m, tea.Quit
		case tea.KeyEsc:
			if m.thinking {
				return m, func() tea.Msg { return cancelMsg{} }
			}
		case tea.KeyEnter:
			line := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if line == "" {
				return m, nil
			}
			if m.thinking {
				// Queue everything (regular + slash) while thinking.
				m.pending = append(m.pending, line)
				m.appendHistoryCapped(tuiStyleDim.Render("[queued] " + line))
				return m, nil
			}
			cmd := m.startTurn(line)
			return m, cmd
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

	case cancelMsg:
		if m.thinking {
			m.thinkTag++ // invalidate any pending thinkingDoneMsg
			m.thinking = false
			elapsed := time.Since(m.thinkStart).Truncate(time.Second)
			m.appendHistoryCapped(tuiStyleThought.Render(fmt.Sprintf("Interrupted (after %s)", elapsed)))
			// Fire OnStop with empty last-assistant-message and stop_hook_active=true.
			cmds = append(cmds, cmdHookStop(m.opts.hooks, "", true))
		}

	case thinkingDoneMsg:
		if !m.thinking || msg.tag != m.thinkTag {
			// Stale tick from a cancelled or superseded turn.
			break
		}
		m.thinking = false
		elapsed := time.Since(m.thinkStart).Truncate(time.Second)
		m.appendHistoryCapped(tuiStyleThought.Render(fmt.Sprintf("Thought for %s", elapsed)))
		m.appendHistoryCapped(msg.response)
		cmds = append(cmds, cmdHookStop(m.opts.hooks, msg.response, false))
		m.count++
		if m.opts.exitAfter > 0 && m.count >= m.opts.exitAfter {
			m.appendHistoryCapped(tuiStyleDim.Render(fmt.Sprintf("[exit-after %d reached]", m.opts.exitAfter)))
			m.quitReason = "other"
			return m, tea.Quit
		}
		// Drain the next pending prompt, if any.
		if len(m.pending) > 0 {
			next := m.pending[0]
			m.pending = m.pending[1:]
			cmd := m.startTurn(next)
			cmds = append(cmds, cmd)
		}

	case slashDoneMsg:
		if msg.rendered != "" {
			// Trim trailing newline so each rendered slash output occupies
			// one history block. Multi-line content keeps its internal newlines.
			m.appendHistoryCapped(strings.TrimRight(msg.rendered, "\n"))
		}
		if msg.outcome.Exit {
			m.quitReason = msg.outcome.Reason
			m.quitCode = msg.outcome.ExitCode
			return m, tea.Quit
		}
		if msg.outcome.Restart {
			// Simulate /clear- or /compact-style reset on the wire only:
			// flush any pending /fake-tool, then SessionEnd then SessionStart
			// with the same matcher value, all in one tea.Cmd goroutine so
			// the ordering is sequential. tea.Batch would run them
			// concurrently and lose the back-to-back contract on the wire.
			// History/scrollback is not cleared — that's a future UI primitive.
			cmds = append(cmds, cmdSlashRestart(m.opts.slash, m.opts.hooks, msg.outcome.RestartReason))
			break
		}
		// /think — run the message through the regular prompt path so hooks
		// fire and the thinking animation runs. Outcome carries the optional
		// duration override.
		if msg.outcome.Prompt != "" || msg.outcome.HasThinkDuration {
			return m, m.startPromptTurn(msg.outcome.Prompt, msg.outcome.ThinkDuration, msg.outcome.HasThinkDuration)
		}

	case hookErrMsg:
		if msg.err != nil {
			m.appendHistoryCapped(tuiStyleDim.Render(fmt.Sprintf("[hook %s error: %v]", msg.stage, msg.err)))
		}

	case mcpConnectMsg:
		if msg.err != nil {
			m.appendHistoryCapped(tuiStyleDim.Render(fmt.Sprintf("[mcp connect failed: %v]", msg.err)))
		} else if msg.tools > 0 {
			m.appendHistoryCapped(tuiStyleDim.Render(fmt.Sprintf("[mcp connected: %d tools]", msg.tools)))
		}
		if msg.startErr != nil {
			m.appendHistoryCapped(tuiStyleDim.Render(fmt.Sprintf("[hook OnSessionStart error: %v]", msg.startErr)))
		}

	case autoExitMsg:
		m.appendHistoryCapped(tuiStyleDim.Render(fmt.Sprintf("[auto-exit after %s]", m.opts.autoExit)))
		m.quitReason = "other"
		return m, tea.Quit

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	// Forward unhandled messages to the textinput (cursor blink etc).
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// startTurn is invoked when the user submits a line and we're not currently
// thinking. It distinguishes slash commands from regular prompts and returns
// the command(s) to run for this turn.
func (m *model) startTurn(line string) tea.Cmd {
	// Echo the user prompt into history (matching the scanner path's "> line").
	m.appendHistoryCapped(tuiStylePrompt.Render("> ") + line)

	if strings.HasPrefix(line, "/") {
		// Slash dispatch. We render synchronously (most slash commands are
		// near-instant; /stream sleeps a few hundred ms which is fine in the
		// tea.Cmd goroutine).
		return cmdSlashDispatch(m.opts.slash, line)
	}

	return m.startPromptTurn(line, 0, false)
}

// startPromptTurn fires UserPromptSubmit + the thinking animation for a
// message. Used by raw-input prompts and by /think (which routes through the
// same code path so it shares hooks + animation behavior). hasOverride
// distinguishes "no duration parsed → use default" from "explicit /think 0 …
// → no thinking, immediate echo."
func (m *model) startPromptTurn(line string, override time.Duration, hasOverride bool) tea.Cmd {
	delay := m.opts.delay
	if hasOverride {
		delay = override
	}

	m.thinking = true
	m.thinkingInput = line
	m.thinkStart = time.Now()
	m.thinkTag++
	tag := m.thinkTag

	response := fmt.Sprintf("[%s] %s", m.opts.name, line)
	return tea.Batch(
		cmdHookPrompt(m.opts.hooks, line, m.opts.name),
		cmdThink(delay, tag, response),
		m.spin.Tick,
	)
}

// View composes the rendered frame: history, optional spinner row, then the
// textinput. Bubbletea handles partial-redraw / diffing; the View just
// produces the full intended frame each tick.
func (m model) View() string {
	var b strings.Builder
	for _, line := range m.history {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if m.thinking {
		elapsed := time.Since(m.thinkStart).Truncate(time.Second)
		b.WriteString(tuiStyleSpin.Render(fmt.Sprintf("%s thinking… (%s · esc to interrupt)", m.spin.View(), elapsed)))
		b.WriteString("\n")
	}
	b.WriteString(m.input.View())
	return b.String()
}

// appendHistoryCapped appends a line (with any trailing newlines stripped so
// View can add its own separator deterministically) and evicts oldest entries
// when the cap is exceeded. cap=0 disables eviction.
func (m *model) appendHistoryCapped(line string) {
	m.history = append(m.history, strings.TrimRight(line, "\n"))
	limit := m.opts.historyCap
	if limit <= 0 {
		return
	}
	if len(m.history) > limit {
		drop := len(m.history) - limit
		m.history = m.history[drop:]
	}
}

// cmdThink returns a tea.Cmd that fires thinkingDoneMsg after delay. The tag
// lets the model ignore the response if the turn was cancelled in the meantime.
func cmdThink(delay time.Duration, tag int, response string) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return thinkingDoneMsg{tag: tag, response: response}
	})
}

// cmdSlashDispatch runs a slash command on a goroutine and returns its
// rendered output + outcome.
func cmdSlashDispatch(slash *SlashHandler, line string) tea.Cmd {
	return func() tea.Msg {
		rendered, outcome := slash.DispatchString(context.Background(), line)
		return slashDoneMsg{rendered: rendered, outcome: outcome}
	}
}

// cmdHookPrompt fires UserPromptSubmit on a goroutine.
func cmdHookPrompt(hooks *HookSender, prompt, name string) tea.Cmd {
	return func() tea.Msg {
		return hookErrMsg{stage: "OnPrompt", err: hooks.OnPrompt(context.Background(), prompt, name)}
	}
}

// cmdHookStop fires Stop on a goroutine.
func cmdHookStop(hooks *HookSender, last string, stopHookActive bool) tea.Cmd {
	return func() tea.Msg {
		return hookErrMsg{stage: "OnStop", err: hooks.OnStop(context.Background(), last, stopHookActive)}
	}
}

// cmdBoot runs the boot sequence — MCP connect, then SessionStart — in one
// goroutine, so SessionStart fires synchronously after the connect attempt
// regardless of whether the bubbletea program is still around to process
// the resulting mcpConnectMsg. This mirrors the scanner path's synchronous
// mcp.Connect → OnSessionStart ordering and prevents races between boot
// SessionStart and user-driven hooks (e.g. /restart) submitted before the
// boot goroutine resolves.
func cmdBoot(mcp *MCPClient, hooks *HookSender, source string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		connectErr := mcp.Connect(ctx)
		tools := 0
		if connectErr == nil {
			tools = len(mcp.Tools())
		}
		startErr := hooks.OnSessionStart(ctx, source)
		return mcpConnectMsg{err: connectErr, tools: tools, startErr: startErr}
	}
}

// cmdSlashRestart performs the /restart sequence in one goroutine so
// PostToolUse (for any pending /fake-tool), SessionEnd, and SessionStart land
// on the wire in that fixed order. tea.Batch would dispatch separate cmds
// concurrently, which would race the SessionEnd/SessionStart POSTs and
// violate the back-to-back contract documented on SlashOutcome.Restart.
func cmdSlashRestart(slash *SlashHandler, hooks *HookSender, reason string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		slash.FlushPendingTool(ctx)
		endErr := hooks.OnSessionEnd(ctx, reason)
		startErr := hooks.OnSessionStart(ctx, reason)
		if err := errors.Join(endErr, startErr); err != nil {
			return hookErrMsg{stage: "OnRestart", err: err}
		}
		return nil
	}
}

// cmdAutoExit returns a tea.Cmd that fires autoExitMsg after d.
func cmdAutoExit(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return autoExitMsg{} })
}

// runTUI runs the bubbletea program and returns (exit code, shutdown reason).
// The reason mirrors the model's quitReason ("logout" for /exit, "other"
// otherwise) so callers can pass it through to the SessionEnd hook. Caller
// supplies quitCh — closing it forwards SIGINT/SIGTERM into p.Quit() once
// without racing with the alt-screen teardown.
func runTUI(ctx context.Context, opts tuiOptions, quitCh <-chan struct{}) (int, string) {
	m := newModel(opts)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))

	if quitCh != nil {
		go func() {
			<-quitCh
			p.Quit()
		}()
	}

	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testagent: tui: %v\n", err)
		return 1, "other"
	}
	if fm, ok := finalModel.(model); ok {
		return fm.quitCode, fm.quitReason
	}
	return 0, "other"
}
