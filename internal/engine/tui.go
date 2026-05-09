// Bubbletea-driven interactive TUI. Used when stdin is a TTY so input
// keystrokes are accepted concurrently with the thinking spinner. Headless
// paths (piped stdin) use runScanner instead.

package engine

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

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/render"
	"github.com/paultyng/testagent/internal/slash"
)

// model is the bubbletea Model driving the interactive session.
type model struct {
	g Globals
	d Deps

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
// name and body let the handler render a styled echo in scrollback while the
// Stop-hook payload (last_assistant_message) keeps the plain "[name] body"
// shape — ANSI codes must not leak into the wire payload.
type thinkingDoneMsg struct {
	tag  int
	name string
	body string
}

// slashDoneMsg fires when an asynchronously-dispatched slash command finishes.
type slashDoneMsg struct {
	rendered string
	outcome  slash.Outcome
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

// newModel builds the initial model. The textinput and spinner are
// bubbles components; both honor m.width on each Update.
func newModel(g Globals, d Deps) model {
	ti := textinput.New()
	ti.Placeholder = ""
	ti.Prompt = render.Prompt()
	ti.Focus()
	ti.CharLimit = 0

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = render.ThinkingStyle

	return model{
		g:     g,
		d:     d,
		input: ti,
		spin:  sp,
	}
}

// banner renders the rounded banner shown once at session start. Same shape
// as the scanner-path banner so users see the same intro across both modes.
// First line reads "<Emulator>: <Name>" — emulator type in the cool banner
// hue, name in the warm session hue, so the type and the user-supplied label
// are visually distinct.
func banner(g Globals) string {
	sessionLabel := "session"
	if g.Resumed {
		sessionLabel = "resumed"
	}
	content := lipgloss.JoinVertical(lipgloss.Left,
		bannerTitle(g.Emulator, g.Name),
		render.BannerMetaStyle.Faint(true).Render(sessionLabel+" "+g.SessionID),
		render.MuteStyle.Render("Type anything; /help for commands"),
	)
	return render.BannerStyle.Render(content)
}

// bannerTitle renders the "<Emulator>: <Name>" first line, dropping the
// prefix if Emulator is empty (callers that don't set it get the prior
// behavior).
func bannerTitle(emulator, name string) string {
	if emulator == "" {
		return render.SessionStyle.Render(name)
	}
	return render.BannerMetaStyle.Render(emulator+": ") + render.SessionStyle.Render(name)
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
	if m.g.Resumed {
		startSource = "resume"
	}
	cmds := []tea.Cmd{
		textinput.Blink,
		m.spin.Tick,
		cmdBoot(m.d.MCP, m.d.Hooks, startSource),
	}
	if m.g.AutoExit > 0 {
		cmds = append(cmds, cmdAutoExit(m.g.AutoExit))
	}
	return tea.Batch(cmds...)
}

// Update is the model's event handler. Bubbletea serializes Update calls so
// no mutex is needed; long-running work is pushed onto goroutines via tea.Cmd.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Lazy banner injection — Init can't mutate state, so we do it once on
	// the first Update tick.
	if !m.bannerDone {
		m.history = append(m.history, banner(m.g))
		if m.g.StatusLine != "" {
			m.history = append(m.history, render.MuteStyle.Render("["+m.g.StatusLine+"]"))
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
				m.appendHistoryCapped(render.MuteStyle.Render("[queued] " + line))
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
			m.appendHistoryCapped(render.ThoughtMarkerStyle.Render(fmt.Sprintf("Interrupted (after %s)", elapsed)))
			// Fire OnStop with empty last-assistant-message and stop_hook_active=true.
			cmds = append(cmds, cmdHookStop(m.d.Hooks, "", true))
		}

	case thinkingDoneMsg:
		if !m.thinking || msg.tag != m.thinkTag {
			// Stale tick from a cancelled or superseded turn.
			break
		}
		m.thinking = false
		elapsed := time.Since(m.thinkStart).Truncate(time.Second)
		m.appendHistoryCapped(render.ThoughtMarkerStyle.Render(fmt.Sprintf("Thought for %s", elapsed)))
		m.appendHistoryCapped(render.Echo(msg.name, msg.body))
		cmds = append(cmds, cmdHookStop(m.d.Hooks, fmt.Sprintf("[%s] %s", msg.name, msg.body), false))
		m.count++
		if m.g.ExitAfter > 0 && m.count >= m.g.ExitAfter {
			m.appendHistoryCapped(render.MuteStyle.Render(fmt.Sprintf("[exit-after %d reached]", m.g.ExitAfter)))
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
			cmds = append(cmds, cmdSlashRestart(m.d.Slash, m.d.Hooks, msg.outcome.RestartReason))
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
			// Hook errors get the LifecycleWarn token (yellow) so they don't
			// vanish into the mute lifecycle-note stream.
			m.appendHistoryCapped(render.LifecycleWarn(fmt.Sprintf("hook %s error: %v", msg.stage, msg.err)))
		}

	case mcpConnectMsg:
		if msg.err != nil {
			m.appendHistoryCapped(render.MuteStyle.Render(fmt.Sprintf("[mcp connect failed: %v]", msg.err)))
		} else if msg.tools > 0 {
			m.appendHistoryCapped(render.MuteStyle.Render(fmt.Sprintf("[mcp connected: %d tools]", msg.tools)))
		}
		if msg.startErr != nil {
			m.appendHistoryCapped(render.LifecycleWarn(fmt.Sprintf("hook OnSessionStart error: %v", msg.startErr)))
		}

	case autoExitMsg:
		m.appendHistoryCapped(render.MuteStyle.Render(fmt.Sprintf("[auto-exit after %s]", m.g.AutoExit)))
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
	m.appendHistoryCapped(render.Prompt() + line)

	if strings.HasPrefix(line, "/") {
		// Slash dispatch. We render synchronously (most slash commands are
		// near-instant; /stream sleeps a few hundred ms which is fine in the
		// tea.Cmd goroutine).
		return cmdSlashDispatch(m.d.Slash, line)
	}

	return m.startPromptTurn(line, 0, false)
}

// startPromptTurn fires UserPromptSubmit + the thinking animation for a
// message. Used by raw-input prompts and by /think (which routes through the
// same code path so it shares hooks + animation behavior). hasOverride
// distinguishes "no duration parsed → use default" from "explicit /think 0 …
// → no thinking, immediate echo."
func (m *model) startPromptTurn(line string, override time.Duration, hasOverride bool) tea.Cmd {
	delay := m.g.Delay
	if hasOverride {
		delay = override
	}

	m.thinking = true
	m.thinkingInput = line
	m.thinkStart = time.Now()
	m.thinkTag++
	tag := m.thinkTag

	return tea.Batch(
		cmdHookPrompt(m.d.Hooks, line, m.g.Name),
		cmdThink(delay, tag, m.g.Name, line),
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
		// Spinner glyph (already styled by m.spin.Style = thinking) +
		// "thinking…" in the same warm token, then the mute parenthetical.
		b.WriteString(m.spin.View())
		b.WriteString(render.Thinking(" thinking…"))
		b.WriteString(render.MuteStyle.Render(fmt.Sprintf(" (%s · esc to interrupt)", elapsed)))
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
	limit := m.g.HistoryCap
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
func cmdThink(delay time.Duration, tag int, name, body string) tea.Cmd {
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return thinkingDoneMsg{tag: tag, name: name, body: body}
	})
}

// cmdSlashDispatch runs a slash command on a goroutine and returns its
// rendered output + outcome.
func cmdSlashDispatch(handler *slash.Handler, line string) tea.Cmd {
	return func() tea.Msg {
		rendered, outcome := handler.DispatchString(context.Background(), line)
		return slashDoneMsg{rendered: rendered, outcome: outcome}
	}
}

// cmdHookPrompt fires UserPromptSubmit on a goroutine.
func cmdHookPrompt(sender *hooks.Sender, prompt, name string) tea.Cmd {
	return func() tea.Msg {
		return hookErrMsg{stage: "OnPrompt", err: sender.OnPrompt(context.Background(), prompt, name)}
	}
}

// cmdHookStop fires Stop on a goroutine.
func cmdHookStop(sender *hooks.Sender, last string, stopHookActive bool) tea.Cmd {
	return func() tea.Msg {
		return hookErrMsg{stage: "OnStop", err: sender.OnStop(context.Background(), last, stopHookActive)}
	}
}

// cmdBoot runs the boot sequence — MCP connect, then SessionStart — in one
// goroutine, so SessionStart fires synchronously after the connect attempt
// regardless of whether the bubbletea program is still around to process
// the resulting mcpConnectMsg. This mirrors the scanner path's synchronous
// mcp.Connect → OnSessionStart ordering and prevents races between boot
// SessionStart and user-driven hooks (e.g. /restart) submitted before the
// boot goroutine resolves.
func cmdBoot(client *mcp.Client, sender *hooks.Sender, source string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		connectErr := client.Connect(ctx)
		tools := 0
		if connectErr == nil {
			tools = len(client.Tools())
		}
		startErr := sender.OnSessionStart(ctx, source)
		return mcpConnectMsg{err: connectErr, tools: tools, startErr: startErr}
	}
}

// cmdSlashRestart performs the /restart sequence in one goroutine so
// PostToolUse (for any pending /fake-tool), SessionEnd, and SessionStart land
// on the wire in that fixed order. tea.Batch would dispatch separate cmds
// concurrently, which would race the SessionEnd/SessionStart POSTs and
// violate the back-to-back contract documented on slash.Outcome.Restart.
func cmdSlashRestart(handler *slash.Handler, sender *hooks.Sender, reason string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		handler.FlushPendingTool(ctx)
		endErr := sender.OnSessionEnd(ctx, reason)
		startErr := sender.OnSessionStart(ctx, reason)
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
func runTUI(ctx context.Context, g Globals, d Deps, quitCh <-chan struct{}) (int, string) {
	m := newModel(g, d)
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
