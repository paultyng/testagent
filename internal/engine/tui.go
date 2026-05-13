// Bubbletea-driven interactive TUI. Used when stdin is a TTY so input
// keystrokes are accepted concurrently with the thinking spinner. Headless
// paths (piped stdin) use runScanner instead.
//
// Rendering model: bubbletea v2 inline mode (no alt-screen). The bubbletea
// program manages a small bottom block — optional spinner row, optional
// streaming line, queue display, multi-line input. Completed content
// (banner, user echoes, completed streamed responses, lifecycle markers)
// commits above the program via tea.Println/Printf and becomes native
// terminal scrollback. /clear and /compact wipe the screen plus scrollback
// via VT escape sequences and re-emit the banner + slash echo.

package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
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

	// pending holds prompts the user submitted while a turn was in
	// flight. Rendered as the "queued: ..." display in the bottom pane,
	// below the spinner (codex pattern). Drained one-at-a-time on
	// streamDoneMsg.
	pending []string

	// scrollback records every line committed above the program block via
	// commit(). Production-wise it's observability for tests; the actual
	// committed content lives in the terminal's native scrollback buffer
	// thanks to tea.Println inside commit().
	scrollback []string

	input textarea.Model
	spin  spinner.Model

	// Turn lifecycle. A turn moves through two phases: thinking (spinner
	// runs for thinkDur) and streaming (per-token echo). turnTag is bumped
	// on each new turn AND on cancel, so any in-flight tick/chunk msg with
	// a stale tag is ignored.
	thinking      bool
	thinkingInput string
	thinkStart    time.Time
	turnTag       int

	// Streaming state, valid only while streaming==true. streamLine is the
	// in-progress assistant line shown in the bottom pane; on streamDoneMsg
	// it commits above the program via tea.Println and clears.
	streaming    bool
	streamTokens []string
	streamIdx    int
	streamFinal  string
	streamDelay  time.Duration
	streamLine   string

	width, height int

	count      int
	quitReason string
	quitCode   int
	bootDone   bool
}

// thinkingDoneMsg fires when the simulated thinking delay elapses for tag.
// The handler closes out the spinner phase ("Thought for Ns" marker), seeds
// the streaming phase with msg.body's tokens, and schedules the first
// streamChunkMsg. The plain "[name] body" payload is captured as streamFinal
// so the eventual streamDoneMsg / cancel can fire the Stop hook with
// ANSI-free text. streamDelay is the per-token interval the streaming phase
// will use for this turn.
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
// commits the assembled line, fires the Stop hook, and drains the pending
// queue.
type streamDoneMsg struct {
	tag  int
	body string // assembled "[Name] body" payload for OnStop
}

// slashDoneMsg fires when an asynchronously-dispatched slash command finishes.
type slashDoneMsg struct {
	rendered string
	outcome  slash.Outcome
}

// hookErrMsg surfaces hook errors that happened on a tea.Cmd goroutine
// (e.g. cmdHookStop). Renders a single warning line in scrollback.
type hookErrMsg struct {
	stage string
	err   error
}

// mcpConnectMsg surfaces the MCP boot + SessionStart outcome. Logged inline
// as scrollback once.
type mcpConnectMsg struct {
	err      error
	tools    int
	startErr error
}

// autoExitMsg fires after --auto-exit's duration elapses.
type autoExitMsg struct{}

// cancelMsg is dispatched by Esc to cancel an in-flight turn.
type cancelMsg struct{}

// newModel builds the initial model. The textarea and spinner are bubbles
// components; both honor m.width on each Update.
func newModel(g Globals, d Deps) model {
	ta := textarea.New()
	ta.Placeholder = ""
	ta.Prompt = render.Prompt()
	ta.ShowLineNumbers = false
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 0   // no cap; expands to fit content
	ta.CharLimit = 0   // unlimited
	// Plain Enter submits; Shift+Enter inserts a newline (matches Claude
	// Code / Codex CLI conventions). The default textarea KeyMap binds
	// Enter to InsertNewline; we move InsertNewline to shift+enter so our
	// Update can keep Enter as the submit key.
	km := textarea.DefaultKeyMap()
	km.InsertNewline = key.NewBinding(
		key.WithKeys("shift+enter"),
		key.WithHelp("shift+enter", "insert newline"),
	)
	ta.KeyMap = km
	// Flatten the focused/blurred cursor-line tint; textarea ships with a
	// full-row background on the active line which makes the input visually
	// distinct from scrollback above. textinput (and real Claude Code's
	// input) don't have one. Override to a plain style so the input row
	// blends with surrounding content.
	styles := ta.Styles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	ta.SetStyles(styles)
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = render.ThinkingStyle

	return model{
		g:     g,
		d:     d,
		input: ta,
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

// Init seeds the initial command batch: spinner ticks, the boot sequence
// (cmdBoot does MCP connect → SessionStart in one goroutine), and optional
// auto-exit timer. The banner is committed via tea.Println on the first
// Update tick (Init can't mutate state, so we use a bootDone latch).
func (m model) Init() tea.Cmd {
	startSource := "startup"
	if m.g.Resumed {
		startSource = "resume"
	}
	cmds := []tea.Cmd{
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
	var cmds []tea.Cmd

	// Lazy boot: commit the banner + status line once via tea.Println so
	// they land in native scrollback at the top of the session. Sequenced
	// so the status line renders under the banner, not above it.
	if !m.bootDone {
		m.bootDone = true
		boot := []tea.Cmd{m.commit(banner(m.g))}
		if m.g.StatusLine != "" {
			boot = append(boot, m.commit(render.MuteStyle.Render("["+m.g.StatusLine+"]")))
		}
		cmds = append(cmds, tea.Sequence(boot...))
	}

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
			m.input.Reset()
			if line == "" {
				return m, tea.Batch(cmds...)
			}
			if m.thinking || m.streaming {
				// Queue everything (regular + slash) while a turn is in
				// flight. The queue lives in the bottom pane (below the
				// spinner) per the codex bottom-pane model. No scrollback
				// commit yet — that happens when the queued line promotes
				// to a real prompt at the start of its turn.
				m.pending = append(m.pending, line)
				return m, tea.Batch(cmds...)
			}
			cmds = append(cmds, m.startTurn(line))
			return m, tea.Batch(cmds...)
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

	case cancelMsg:
		if m.thinking {
			m.turnTag++ // invalidate any pending thinkingDoneMsg
			m.thinking = false
			elapsed := time.Since(m.thinkStart).Truncate(time.Second)
			cmds = append(cmds, m.commit(render.ThoughtMarkerStyle.Render(fmt.Sprintf("Interrupted (after %s)", elapsed))))
			cmds = append(cmds, cmdHookStop(m.d.Hooks, "", true))
		} else if m.streaming {
			m.turnTag++ // invalidate any pending streamChunkMsg
			m.streaming = false
			// Reconstruct the plain-text partial body from the tokens
			// that were emitted before cancel.
			partial := fmt.Sprintf("[%s] %s", m.g.Name, strings.Join(m.streamTokens[:m.streamIdx], " "))
			// Commit whatever streamed before the cancel, then the
			// interrupt marker.
			if m.streamLine != "" {
				cmds = append(cmds, m.commit(m.streamLine))
			}
			cmds = append(cmds, m.commit(render.ThoughtMarkerStyle.Render("Interrupted")))
			cmds = append(cmds, cmdHookStop(m.d.Hooks, partial, true))
			m.streamTokens = nil
			m.streamLine = ""
		}

	case thinkingDoneMsg:
		if !m.thinking || msg.tag != m.turnTag {
			break
		}
		m.thinking = false
		elapsed := time.Since(m.thinkStart).Truncate(time.Second)
		cmds = append(cmds, m.commit(render.ThoughtMarkerStyle.Render(fmt.Sprintf("Thought for %s", elapsed))))
		// Transition into streaming. streamLine is the growing in-progress
		// echo shown in the bottom pane; it commits via tea.Println on
		// streamDoneMsg.
		m.streaming = true
		m.streamTokens = strings.Fields(msg.body)
		m.streamIdx = 0
		m.streamFinal = fmt.Sprintf("[%s] %s", msg.name, msg.body)
		m.streamDelay = msg.streamDelay
		m.streamLine = render.EchoHeader(msg.name)
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
		m.streamLine = m.streamLine + " " + tok
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
		// Commit the completed streamed line above the program.
		if m.streamLine != "" {
			cmds = append(cmds, m.commit(m.streamLine))
		}
		m.streamTokens = nil
		m.streamLine = ""
		cmds = append(cmds, cmdHookStop(m.d.Hooks, msg.body, false))
		m.count++
		if m.g.ExitAfter > 0 && m.count >= m.g.ExitAfter {
			cmds = append(cmds, m.commit(render.MuteStyle.Render(fmt.Sprintf("[exit-after %d reached]", m.g.ExitAfter))))
			m.quitReason = "other"
			cmds = append(cmds, tea.Quit)
			return m, tea.Batch(cmds...)
		}
		// Drain the next pending prompt, if any.
		if len(m.pending) > 0 {
			next := m.pending[0]
			m.pending = m.pending[1:]
			cmds = append(cmds, m.startTurn(next))
		}

	case slashDoneMsg:
		if msg.rendered != "" {
			// Trim trailing newline so the commit is one block.
			cmds = append(cmds, m.commit(strings.TrimRight(msg.rendered, "\n")))
		}
		if msg.outcome.Exit {
			m.quitReason = msg.outcome.Reason
			m.quitCode = msg.outcome.ExitCode
			cmds = append(cmds, tea.Quit)
			return m, tea.Batch(cmds...)
		}
		if msg.outcome.Restart {
			// /clear and /compact wipe screen + scrollback (VT escape
			// sequences ESC[2J ESC[3J ESC[H — same shape codex and the
			// pre-fullscreen Claude Code use), then re-emit the banner,
			// status line, and the user-echo for the slash that triggered
			// the lifecycle. /compact additionally prints a Compacted
			// marker. Hook lifecycle (PreCompact → SessionEnd →
			// SessionStart → PostCompact) fires via cmdSlashRestart.
			// ESC[3J clears the scrollback buffer (xterm extension);
			// ESC[2J clears the visible screen; ESC[H homes the cursor.
			// Matches the sequence codex emits on /clear and the
			// pre-fullscreen Claude Code TUI. Also reset the test-
			// observable scrollback slice so it reflects what's visible
			// to the user after the wipe.
			m.scrollback = nil
			// Order matters: wipe MUST land before the re-emit, otherwise
			// the banner can race the escape and disappear off-screen.
			// tea.Batch has no ordering guarantees within its cmds, so the
			// wipe + re-emit chain runs through tea.Sequence.
			//
			// Two-step wipe: ESC[3J clears the xterm scrollback buffer
			// (no bubbletea primitive exists for this), then tea.ClearScreen
			// clears the visible screen the proper way. Using a single Printf
			// with the full escape (including cursor-home) confused
			// bubbletea's internal cursor tracking and left the textarea
			// prompt rendering above the bottom instead of in it.
			redraw := []tea.Cmd{
				tea.Printf("\x1b[3J"),
				tea.ClearScreen,
				m.commit(banner(m.g)),
			}
			if m.g.StatusLine != "" {
				redraw = append(redraw, m.commit(render.MuteStyle.Render("["+m.g.StatusLine+"]")))
			}
			redraw = append(redraw, m.commit(render.Prompt()+"/"+slashName(msg.outcome.RestartReason, msg.outcome.CompactTrigger)))
			if msg.outcome.RestartReason == "compact" {
				redraw = append(redraw, m.commit(render.ThoughtMarker("Compacted")))
			}
			cmds = append(cmds, tea.Sequence(redraw...))
			cmds = append(cmds, cmdSlashRestart(m.d.Slash, m.d.Hooks, msg.outcome.RestartReason, msg.outcome.CompactTrigger))
			return m, tea.Batch(cmds...)
		}
		// /think or /stream — run the message through the regular prompt
		// path so hooks fire and the thinking animation + streamed echo
		// run. Outcome carries the duration overrides.
		if msg.outcome.Prompt != "" {
			cmds = append(cmds, m.startPromptTurn(
				msg.outcome.Prompt,
				msg.outcome.ThinkDuration, msg.outcome.HasThinkDuration,
				msg.outcome.StreamDuration, msg.outcome.HasStreamDuration,
			))
			return m, tea.Batch(cmds...)
		}

	case hookErrMsg:
		if msg.err != nil {
			cmds = append(cmds, m.commit(render.LifecycleWarn(fmt.Sprintf("hook %s error: %v", msg.stage, msg.err))))
		}

	case mcpConnectMsg:
		if msg.err != nil {
			cmds = append(cmds, m.commit(render.MuteStyle.Render(fmt.Sprintf("[mcp connect failed: %v]", msg.err))))
		} else if msg.tools > 0 {
			cmds = append(cmds, m.commit(render.MuteStyle.Render(fmt.Sprintf("[mcp connected: %d tools]", msg.tools))))
		}
		if msg.startErr != nil {
			cmds = append(cmds, m.commit(render.LifecycleWarn(fmt.Sprintf("hook OnSessionStart error: %v", msg.startErr))))
		}

	case autoExitMsg:
		cmds = append(cmds, m.commit(render.MuteStyle.Render(fmt.Sprintf("[auto-exit after %s]", m.g.AutoExit))))
		m.quitReason = "other"
		cmds = append(cmds, tea.Quit)
		return m, tea.Batch(cmds...)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)
	}

	// Forward unhandled messages to the textarea (cursor blink etc).
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// commit emits a line above the program block via tea.Println AND records
// it in m.scrollback for test observability. Use for anything that should
// land in native terminal scrollback — banner, user echoes, completed
// streamed responses, lifecycle markers. Raw escape sequences (e.g. the
// /clear ESC[3J wipe) still use tea.Printf directly since they aren't
// "lines" in the scrollback sense.
func (m *model) commit(line string) tea.Cmd {
	m.scrollback = append(m.scrollback, line)
	return tea.Println(line)
}

// slashName returns the slash-command-as-typed for display when /clear or
// /compact re-emits the user echo after wiping the screen. /fake-auto-compact
// uses a CompactTrigger of "auto"; everything else maps from RestartReason.
func slashName(restartReason, compactTrigger string) string {
	if restartReason == "compact" && compactTrigger == "auto" {
		return "fake-auto-compact"
	}
	return restartReason
}

// startTurn is invoked when the user submits a line and we're not currently
// thinking. It distinguishes slash commands from regular prompts and returns
// the command(s) to run for this turn. The user echo commits via tea.Println.
func (m *model) startTurn(line string) tea.Cmd {
	cmds := []tea.Cmd{
		m.commit(render.Prompt() + line),
	}

	if strings.HasPrefix(line, "/") {
		// Slash dispatch.
		cmds = append(cmds, cmdSlashDispatch(m.d.Slash, line))
		return tea.Batch(cmds...)
	}

	cmds = append(cmds, m.startPromptTurn(line, 0, false, 0, false))
	return tea.Batch(cmds...)
}

// startPromptTurn kicks off the thinking → streaming pipeline for a regular
// prompt. Used by both startTurn (raw input) and the /think / /stream slash
// paths (which supply explicit thinkDur / streamDur overrides).
func (m *model) startPromptTurn(prompt string, thinkDur time.Duration, hasThink bool, streamDur time.Duration, hasStream bool) tea.Cmd {
	m.turnTag++
	m.thinking = true
	m.thinkingInput = prompt
	m.thinkStart = time.Now()

	dur := m.g.ThinkDelay
	if hasThink {
		dur = thinkDur
	}
	streamD := m.g.StreamDelay
	if hasStream {
		streamD = streamDur
	}

	body := prompt
	return tea.Batch(
		cmdHookPrompt(m.d.Hooks, prompt, m.g.Name),
		cmdThink(dur, streamD, m.turnTag, m.g.Name, body),
	)
}

// View renders the bottom pane only. Inline mode (no AltScreen) — completed
// content lives in native terminal scrollback above the program block.
// Bottom pane composition: optional spinner row, optional streaming line,
// queue display, multi-line input.
func (m model) View() tea.View {
	var b strings.Builder
	if m.thinking {
		elapsed := time.Since(m.thinkStart).Truncate(time.Second)
		b.WriteString(m.spin.View())
		b.WriteString(render.Thinking(" thinking…"))
		b.WriteString(render.MuteStyle.Render(fmt.Sprintf(" (%s · esc to interrupt)", elapsed)))
		b.WriteString("\n")
	}
	if m.streaming && m.streamLine != "" {
		b.WriteString(m.streamLine)
		b.WriteString("\n")
	}
	for _, p := range m.pending {
		b.WriteString(render.MuteStyle.Render("  queued: " + p))
		b.WriteString("\n")
	}
	b.WriteString(m.input.View())
	return tea.NewView(b.String())
}

func cmdThink(delay, streamDelay time.Duration, tag int, name, body string) tea.Cmd {
	if delay <= 0 {
		// Synchronous fast-path: dispatch the thinkingDoneMsg without going
		// through a timer goroutine. Keeps deterministic ordering in tests
		// and snappier behavior when callers want "no spinner".
		return func() tea.Msg {
			return thinkingDoneMsg{tag: tag, name: name, body: body, streamDelay: streamDelay}
		}
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return thinkingDoneMsg{tag: tag, name: name, body: body, streamDelay: streamDelay}
	})
}

func cmdStreamChunk(delay time.Duration, tag int) tea.Cmd {
	if delay <= 0 {
		return func() tea.Msg { return streamChunkMsg{tag: tag} }
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return streamChunkMsg{tag: tag}
	})
}

func cmdStreamDone(tag int, body string) tea.Cmd {
	return func() tea.Msg {
		return streamDoneMsg{tag: tag, body: body}
	}
}

func cmdSlashDispatch(handler *slash.Handler, line string) tea.Cmd {
	return func() tea.Msg {
		rendered, outcome := handler.DispatchString(context.Background(), line)
		return slashDoneMsg{rendered: rendered, outcome: outcome}
	}
}

func cmdHookPrompt(sender HookSender, prompt, name string) tea.Cmd {
	return func() tea.Msg {
		if err := sender.OnPrompt(context.Background(), prompt, name); err != nil {
			return hookErrMsg{stage: "OnPrompt", err: err}
		}
		return nil
	}
}

func cmdHookStop(sender HookSender, last string, stopHookActive bool) tea.Cmd {
	return func() tea.Msg {
		if err := sender.OnStop(context.Background(), last, stopHookActive); err != nil {
			return hookErrMsg{stage: "OnStop", err: err}
		}
		return nil
	}
}

// cmdBoot runs the boot sequence — MCP connect, then SessionStart — in one
// goroutine, so SessionStart fires synchronously after the connect attempt
// regardless of whether the bubbletea program is still around to process
// the resulting mcpConnectMsg. This mirrors the scanner path's synchronous
// mcp.Connect → OnSessionStart ordering and prevents races between boot
// SessionStart and user-driven hooks (e.g. /clear, /compact) submitted before the
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

// cmdSlashRestart performs the /clear or /compact lifecycle in one
// goroutine so PostToolUse (for any pending /fake-tool), the optional
// PreCompact, SessionEnd, SessionStart, and the optional PostCompact land
// on the wire in that fixed order. tea.Batch would dispatch separate cmds
// concurrently, which would race the POSTs and violate the back-to-back
// contract documented on slash.Outcome.Restart.
//
// compactTrigger is empty for /clear (no Pre/PostCompact emission) and
// "manual" or "auto" for /compact or /fake-auto-compact.
func cmdSlashRestart(handler *slash.Handler, sender HookSender, reason, compactTrigger string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		handler.FlushPendingTool(ctx)
		var preErr, postErr error
		if compactTrigger != "" {
			preErr = sender.OnPreCompact(ctx, compactTrigger)
		}
		endErr := sender.OnSessionEnd(ctx, reason)
		startErr := sender.OnSessionStart(ctx, reason)
		if compactTrigger != "" {
			postErr = sender.OnPostCompact(ctx, compactTrigger)
		}
		if err := errors.Join(preErr, endErr, startErr, postErr); err != nil {
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
// for SIGINT, EOF, /auto-exit, etc.).
//
// quitCh receives a struct{} when the outer engine wants the TUI to exit
// without ceremony. The program's Quit listener cancels its own context
// without racing with the alt-screen teardown.
func runTUI(ctx context.Context, g Globals, d Deps, quitCh <-chan struct{}) (int, string) {
	m := newModel(g, d)
	p := tea.NewProgram(m,
		tea.WithContext(ctx),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)
	if quitCh != nil {
		go func() {
			<-quitCh
			p.Quit()
		}()
	}
	out, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testagent: tui error: %v\n", err)
		return 1, "other"
	}
	final, ok := out.(model)
	if !ok {
		return 1, "other"
	}
	reason := final.quitReason
	if reason == "" {
		reason = "other"
	}
	return final.quitCode, reason
}
