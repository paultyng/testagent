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

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/render"
	"github.com/paultyng/testagent/internal/slash"
)

// model is the bubbletea Model driving the interactive session.
type model struct {
	g Globals
	d Deps

	history []string // rendered scrollback
	pending []string // queued prompts submitted while a turn is in flight

	input textinput.Model
	spin  spinner.Model

	// Turn lifecycle. A turn moves through two phases: thinking (spinner
	// runs for thinkDur) and streaming (per-token echo). turnTag is bumped
	// on each new turn AND on cancel, so any in-flight tick/chunk msg with
	// a stale tag is ignored.
	thinking      bool
	thinkingInput string // current prompt being processed
	thinkStart    time.Time
	turnTag       int

	// Streaming state, valid only while streaming==true.
	streaming     bool
	streamTokens  []string // tokens of the assembled echo body (post-name)
	streamIdx     int      // next token index to emit
	streamLineIdx int      // index in m.history of the line being grown
	streamFinal   string   // full "[Name] body" — payload for the Stop hook
	streamDelay   time.Duration

	width, height int

	count      int
	quitReason string
	quitCode   int
	bannerDone bool
}

// thinkingDoneMsg fires when the simulated thinking delay elapses for tag.
// The handler closes out the spinner phase ("Thought for Ns" marker), seeds
// the streaming phase by appending an EchoHeader placeholder line and
// tokenizing body, then schedules the first streamChunkMsg. The plain
// "[name] body" payload is captured here as streamFinal so the eventual
// streamDoneMsg / cancel can fire the Stop hook with ANSI-free text.
// streamDelay is the per-token interval the streaming phase will use for
// this turn.
type thinkingDoneMsg struct {
	tag         int
	name        string
	body        string
	streamDelay time.Duration
}

// streamChunkMsg ticks once per token during the streaming phase. tag
// matches the turnTag at scheduling time; stale chunks (cancelled or
// superseded turns) are dropped.
type streamChunkMsg struct {
	tag int
}

// streamDoneMsg fires after the last token has been appended. The handler
// fires the Stop hook and drains the pending queue.
type streamDoneMsg struct {
	tag  int
	body string // assembled "[Name] body" payload for OnStop
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

// cancelMsg fires when the user presses Esc during an in-flight turn
// (either thinking or streaming).
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
		m.input.SetWidth(w)

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			m.quitReason = "other"
			return m, tea.Quit
		case "esc":
			if m.thinking || m.streaming {
				return m, func() tea.Msg { return cancelMsg{} }
			}
		case "enter":
			line := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if line == "" {
				return m, nil
			}
			if m.thinking || m.streaming {
				// Queue everything (regular + slash) while a turn is in flight.
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
			m.turnTag++ // invalidate any pending thinkingDoneMsg
			m.thinking = false
			elapsed := time.Since(m.thinkStart).Truncate(time.Second)
			m.appendHistoryCapped(render.ThoughtMarkerStyle.Render(fmt.Sprintf("Interrupted (after %s)", elapsed)))
			// Fire OnStop with empty last-assistant-message and stop_hook_active=true.
			cmds = append(cmds, cmdHookStop(m.d.Hooks, "", true))
		} else if m.streaming {
			m.turnTag++ // invalidate any pending streamChunkMsg
			m.streaming = false
			// Reconstruct the plain-text partial body from the tokens that
			// were emitted before cancel — reading the styled history line
			// would leak ANSI codes into the hook payload.
			partial := fmt.Sprintf("[%s] %s", m.g.Name, strings.Join(m.streamTokens[:m.streamIdx], " "))
			m.appendHistoryCapped(render.ThoughtMarkerStyle.Render("Interrupted"))
			cmds = append(cmds, cmdHookStop(m.d.Hooks, partial, true))
			m.streamTokens = nil
		}

	case thinkingDoneMsg:
		if !m.thinking || msg.tag != m.turnTag {
			// Stale tick from a cancelled or superseded turn.
			break
		}
		m.thinking = false
		elapsed := time.Since(m.thinkStart).Truncate(time.Second)
		m.appendHistoryCapped(render.ThoughtMarkerStyle.Render(fmt.Sprintf("Thought for %s", elapsed)))
		// Transition into streaming. Append the echo header as a placeholder
		// line and grow it token-by-token via streamChunkMsg.
		m.streaming = true
		m.streamTokens = strings.Fields(msg.body)
		m.streamIdx = 0
		m.streamFinal = fmt.Sprintf("[%s] %s", msg.name, msg.body)
		m.streamDelay = msg.streamDelay
		m.appendHistoryCapped(render.EchoHeader(msg.name))
		m.streamLineIdx = len(m.history) - 1
		// If the body is empty (e.g., explicit /think 5s "") skip straight
		// to streamDoneMsg so Stop fires consistently.
		if len(m.streamTokens) == 0 {
			cmds = append(cmds, cmdStreamDone(m.turnTag, m.streamFinal))
		} else {
			cmds = append(cmds, cmdStreamChunk(m.streamDelay, m.turnTag))
		}

	case streamChunkMsg:
		if !m.streaming || msg.tag != m.turnTag {
			break
		}
		if m.streamIdx >= len(m.streamTokens) {
			cmds = append(cmds, cmdStreamDone(m.turnTag, m.streamFinal))
			break
		}
		tok := m.streamTokens[m.streamIdx]
		m.streamIdx++
		// Append the token (with a leading space if not the first) to the
		// growing history line. If the line index has shifted due to history-
		// cap rotation between ticks, fall back to creating a new line.
		if m.streamLineIdx < 0 || m.streamLineIdx >= len(m.history) {
			m.appendHistoryCapped(render.EchoHeader(m.g.Name) + " " + tok)
			m.streamLineIdx = len(m.history) - 1
		} else {
			m.history[m.streamLineIdx] = m.history[m.streamLineIdx] + " " + tok
		}
		if m.streamIdx < len(m.streamTokens) {
			cmds = append(cmds, cmdStreamChunk(m.streamDelay, m.turnTag))
		} else {
			cmds = append(cmds, cmdStreamDone(m.turnTag, m.streamFinal))
		}

	case streamDoneMsg:
		if !m.streaming || msg.tag != m.turnTag {
			break
		}
		m.streaming = false
		m.streamTokens = nil
		cmds = append(cmds, cmdHookStop(m.d.Hooks, msg.body, false))
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
		// /think or /stream — run the message through the regular prompt
		// path so hooks fire and the thinking animation + streamed echo
		// run. Outcome carries the duration overrides.
		if msg.outcome.Prompt != "" {
			return m, m.startPromptTurn(
				msg.outcome.Prompt,
				msg.outcome.ThinkDuration, msg.outcome.HasThinkDuration,
				msg.outcome.StreamDuration, msg.outcome.HasStreamDuration,
			)
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

	return m.startPromptTurn(line, 0, false, 0, false)
}

// startPromptTurn fires UserPromptSubmit + the thinking animation for a
// message. Used by raw-input prompts and by /think and /stream (which route
// through the same code path so they share hooks + animation behavior).
// thinkOverride / hasThinkOverride and streamOverride / hasStreamOverride
// let callers swap in per-turn durations; absent overrides use the engine
// globals.
func (m *model) startPromptTurn(
	line string,
	thinkOverride time.Duration, hasThinkOverride bool,
	streamOverride time.Duration, hasStreamOverride bool,
) tea.Cmd {
	thinkDur := m.g.ThinkDelay
	if hasThinkOverride {
		thinkDur = thinkOverride
	}
	streamDur := m.g.StreamDelay
	if hasStreamOverride {
		streamDur = streamOverride
	}

	m.thinking = true
	m.thinkingInput = line
	m.thinkStart = time.Now()
	m.turnTag++
	tag := m.turnTag

	return tea.Batch(
		cmdHookPrompt(m.d.Hooks, line, m.g.Name),
		cmdThink(thinkDur, streamDur, tag, m.g.Name, line),
		m.spin.Tick,
	)
}

// View composes the rendered frame: history, optional spinner row, then the
// textinput. Bubbletea handles partial-redraw / diffing; the View just
// produces the full intended frame each tick. In bubbletea v2 View returns a
// tea.View struct; AltScreen and other terminal-mode toggles live as fields
// on this value (previously imperative tea.WithAltScreen()).
func (m model) View() tea.View {
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
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// appendHistoryCapped appends a line (with any trailing newlines stripped so
// View can add its own separator deterministically) and evicts oldest entries
// when the cap is exceeded. cap=0 disables eviction. When eviction shifts
// the slice down, m.streamLineIdx is rebased so an in-flight stream's
// growing line still points at the same row (or goes negative, in which case
// the chunk handler's bounds check creates a new line).
func (m *model) appendHistoryCapped(line string) {
	m.history = append(m.history, strings.TrimRight(line, "\n"))
	limit := m.g.HistoryCap
	if limit <= 0 {
		return
	}
	if len(m.history) > limit {
		drop := len(m.history) - limit
		m.history = m.history[drop:]
		if m.streaming {
			m.streamLineIdx -= drop
		}
	}
}

// cmdThink returns a tea.Cmd that fires thinkingDoneMsg after delay. The tag
// lets the model ignore the response if the turn was cancelled in the
// meantime. streamDelay rides along so the streaming phase honors the
// per-turn override without a second flag plumbing dance. delay<=0 fires
// immediately on the bubbletea event loop (tea.Tick's behavior with a
// zero duration is implementation-defined).
func cmdThink(delay, streamDelay time.Duration, tag int, name, body string) tea.Cmd {
	if delay <= 0 {
		return func() tea.Msg {
			return thinkingDoneMsg{tag: tag, name: name, body: body, streamDelay: streamDelay}
		}
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return thinkingDoneMsg{tag: tag, name: name, body: body, streamDelay: streamDelay}
	})
}

// cmdStreamChunk schedules the next streamChunkMsg after delay. delay==0
// fires the next chunk immediately on the bubbletea event loop (no
// perceptible pause).
func cmdStreamChunk(delay time.Duration, tag int) tea.Cmd {
	if delay <= 0 {
		return func() tea.Msg { return streamChunkMsg{tag: tag} }
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return streamChunkMsg{tag: tag}
	})
}

// cmdStreamDone fires streamDoneMsg immediately. body is the assembled
// "[Name] message" payload that the OnStop hook receives.
func cmdStreamDone(tag int, body string) tea.Cmd {
	return func() tea.Msg { return streamDoneMsg{tag: tag, body: body} }
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
func cmdHookPrompt(sender HookSender, prompt, name string) tea.Cmd {
	return func() tea.Msg {
		return hookErrMsg{stage: "OnPrompt", err: sender.OnPrompt(context.Background(), prompt, name)}
	}
}

// cmdHookStop fires Stop on a goroutine.
func cmdHookStop(sender HookSender, last string, stopHookActive bool) tea.Cmd {
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
func cmdBoot(client *mcp.Client, sender HookSender, source string) tea.Cmd {
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
func cmdSlashRestart(handler *slash.Handler, sender HookSender, reason string) tea.Cmd {
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
	// v2: WithAltScreen() is gone — AltScreen is now a declarative field on
	// tea.View returned by m.View().
	p := tea.NewProgram(m, tea.WithContext(ctx))

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
