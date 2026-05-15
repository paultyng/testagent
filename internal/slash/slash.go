// Package slash implements the slash-command grammar for driving UI
// primitives interactively.
//
// During an interactive session, lines starting with "/" are interpreted as
// directives that synthesize specific UI elements (streamed text, tool-use
// blocks, panels, MCP calls) instead of going through the default echo path.
package slash

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paultyng/testagent/internal/hookresult"
	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/render"
)

// ToolHookSender is the slash dispatcher's interface to vendor-specific
// PreToolUse / PostToolUse hook delivery. Defined at the consumer site per
// Go conventions; engine.HookSender is a superset (so values held there
// satisfy this directly), and both internal/hooks.Sender (claude HTTP)
// and internal/codexhooks.Runner (codex shell-command) implement it.
type ToolHookSender interface {
	OnPreToolUse(ctx context.Context, toolUseID, toolName string, toolInput any) (hookresult.Result, error)
	OnPostToolUse(ctx context.Context, toolUseID, toolName string, toolInput, toolResponse any, durationMs int64) error
}

// Compile-time assertion that the canonical claude HTTP sender
// satisfies the slash interface. internal/codexhooks.Runner has its
// own assertion in that package.
var _ ToolHookSender = (*hooks.Sender)(nil)

// Handler dispatches slash commands and renders their output.
type Handler struct {
	hooks ToolHookSender
	mcp   *mcp.Client
	out   io.Writer

	// pendingToolMu guards pendingTool. Slash dispatches can run concurrently
	// in TUI mode (cmdSlashDispatch is a tea.Cmd goroutine), so the
	// /fake-tool→/fake-tool-result pairing state needs synchronization.
	pendingToolMu sync.Mutex
	pendingTool   *pendingToolCall
}

// New returns a Handler wired with the supplied dependencies.
func New(sender ToolHookSender, client *mcp.Client, out io.Writer) *Handler {
	return &Handler{
		hooks: sender,
		mcp:   client,
		out:   out,
	}
}

// pendingToolCall tracks a /fake-tool that has not been completed by /fake-tool-result yet.
// /fake-tool fires PreToolUse immediately (tool_input only); the matching
// /fake-tool-result fires PostToolUse with the captured tool_input plus
// the supplied tool_response and the measured duration. /fake-tool
// followed by /fake-tool flushes the prior with empty response and starts
// a new Pre→Post cycle; shutdown flushes whatever's left.
//
// awaitingPermission is set when PreToolUse returned permissionDecision=ask
// (claude only); /fake-tool-result short-circuits in that state, and
// shutdown flushes with a synthetic blocked PostToolUse.
type pendingToolCall struct {
	toolUseID          string
	name               string
	input              any
	startedAt          time.Time
	awaitingPermission bool
	permissionReason   string
}

// Outcome reports control-flow signals from a slash command.
type Outcome struct {
	Handled  bool // input started with "/" and was dispatched
	Exit     bool // session should end (only set by /exit)
	ExitCode int  // exit status when Exit is true
	Reason   string

	// Restart, when true, signals the caller to fire SessionEnd then
	// SessionStart back-to-back without leaving the process. Set by
	// /clear and /compact (and /fake-auto-compact). RestartReason is
	// the matcher value passed through to both events (SessionEnd
	// reason / SessionStart source) — "clear" or "compact". When
	// CompactTrigger is non-empty, the lifecycle additionally wraps
	// SessionEnd/SessionStart with PreCompact and PostCompact events
	// carrying that trigger value ("manual" for /compact, "auto" for
	// /fake-auto-compact).
	Restart        bool
	RestartReason  string
	CompactTrigger string

	// Prompt, when non-empty, signals the caller to run this slash command
	// through the regular prompt-handling path (UserPromptSubmit hook →
	// thinking animation → token-streamed echo → Stop hook). Set by /think
	// and /stream after parsing the required leading duration.
	Prompt string

	// ThinkDuration, when HasThinkDuration is true, overrides the caller's
	// default per-turn thinking time. Set by /think.
	ThinkDuration    time.Duration
	HasThinkDuration bool

	// StreamDuration, when HasStreamDuration is true, overrides the caller's
	// default per-token stream interval. Set by /stream. Zero is allowed
	// (instant emit, no per-token delay).
	StreamDuration    time.Duration
	HasStreamDuration bool
}

// DispatchString is the TUI-friendly entry point. It captures all rendered
// output as a string (so the model can commit it above the program block
// via tea.Println) and returns it alongside the control-flow outcome.
// Concurrent-safe: each call writes to its own buffer, so multiple
// in-flight goroutines never interfere.
func (h *Handler) DispatchString(ctx context.Context, line string) (string, Outcome) {
	var buf bytes.Buffer
	outcome := h.dispatchTo(ctx, line, &buf)
	return buf.String(), outcome
}

// Dispatch parses and runs a single line, writing rendered output to h.out.
// If line doesn't start with "/", returns Handled=false. Errors go to stderr.
func (h *Handler) Dispatch(ctx context.Context, line string) Outcome {
	return h.dispatchTo(ctx, line, h.out)
}

// dispatchTo is the shared dispatch core. All cmd* methods write to the
// passed-in writer rather than h.out so callers can safely run concurrent
// dispatches without sharing per-handler state.
func (h *Handler) dispatchTo(ctx context.Context, line string, out io.Writer) Outcome {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return Outcome{}
	}
	cmd, rest := splitFirstWord(line[1:])
	rest = strings.TrimSpace(rest)

	switch cmd {
	case "help", "?":
		h.cmdHelp(out)
	case "stream":
		return h.cmdStream(out, rest)
	case "think":
		return h.cmdThink(out, rest)
	case "panel":
		h.cmdPanel(out, rest)
	case "link":
		h.cmdLink(out, rest)
	case "fake-tool":
		h.cmdFakeTool(ctx, out, rest)
	case "fake-tool-result":
		h.cmdFakeToolResult(ctx, out, rest)
	case "mcp-call":
		h.cmdMCP(ctx, out, rest)
	case "clear":
		// Real Claude/Codex `/clear` clears the terminal AND starts a new
		// chat. testagent emits the wire-level hook side-effect (SessionEnd
		// → SessionStart with reason="clear") and skips the screen wipe.
		return h.cmdClear(out)
	case "compact":
		// Real Claude/Codex `/compact` triggers context summarization.
		// testagent wraps SessionEnd → SessionStart with PreCompact and
		// PostCompact carrying trigger="manual".
		return h.cmdCompact(out, "manual")
	case "fake-auto-compact":
		// Emulation-only command (no upstream equivalent): drives the
		// compact lifecycle with trigger="auto" so orchestrators can
		// exercise the auto-compact path that real Claude/Codex fires
		// internally on context fill. The `/fake-*` prefix flags this as
		// not a real-user command.
		return h.cmdCompact(out, "auto")
	case "exit", "quit":
		// Codex aliases /quit to /exit; both exit the CLI.
		code := 0
		if rest != "" {
			if n, err := strconv.Atoi(rest); err == nil {
				code = n
			}
		}
		return Outcome{Handled: true, Exit: true, ExitCode: code, Reason: "logout"}
	default:
		fmt.Fprintf(os.Stderr, "testagent: unknown slash command %q (try /help)\n", "/"+cmd)
	}
	return Outcome{Handled: true}
}

// /help — list slash commands with their argument signatures.
func (h *Handler) cmdHelp(out io.Writer) {
	header := render.ToolStyle.Render("slash commands")
	fmt.Fprintln(out, header)
	for _, line := range []struct {
		usage, doc string
	}{
		{"/clear", "fires SessionEnd then SessionStart with reason=clear"},
		{"/compact", "fires PreCompact, SessionEnd, SessionStart, PostCompact with trigger=manual"},
		{"/exit [code]", "exits testagent (default code 0; alias /quit)"},
		{"/fake-auto-compact", "same lifecycle as /compact but with trigger=auto (emulates upstream's internal context-fill trigger)"},
		{`/fake-tool <name> <json-args>`, "prints a fake tool-use block and fires PreToolUse; pair with /fake-tool-result to fire PostToolUse"},
		{`/fake-tool-result <json-or-text>`, "completes the pending /fake-tool and fires PostToolUse with the response"},
		{"/help", "prints this list"},
		{"/link <url> [text]", "prints an OSC 8 hyperlink (clickable in supporting terminals); text defaults to url"},
		{`/mcp-call <server.tool> <json-args>`, "calls a connected MCP tool and prints its result"},
		{"/panel <text>", "prints text in a rounded-border box"},
		{"/quit [code]", "alias of /exit"},
		{`/stream <duration> <message>`, "prompts as if typed raw, with the per-token stream interval overridden"},
		{`/think <duration> <message>`, "prompts as if typed raw, with the thinking-spinner duration overridden"},
	} {
		fmt.Fprintf(out, "  %-40s %s\n",
			render.MuteStyle.Render(line.usage),
			render.MuteSoftStyle.Render(line.doc))
	}
	fmt.Fprintln(out, render.MuteSoftStyle.Render("input not starting with / is echoed back."))
}

// /think <duration> <message> — routes <message> through the regular
// prompt-handling path (UserPromptSubmit → thinking animation → token-
// streamed echo → Stop) with the spinner duration overridden.
//
// Duration is required: bare /think (or /think with no parseable duration)
// writes a usage line to out and returns Handled=true with no Prompt so
// the caller treats it as a pure side effect.
func (h *Handler) cmdThink(out io.Writer, rest string) Outcome {
	dur, msg, ok := parseDurationPrefix(rest)
	if !ok || msg == "" {
		// Plain text — no styling — so piped consumers don't see ANSI on
		// stdout (per AGENTS.md "Debug output goes to stderr ... never
		// ANSI-styled"; usage lines aren't debug, but the same hygiene
		// applies to stdout fragments).
		fmt.Fprintln(out, "usage: /think <duration> <message>")
		return Outcome{Handled: true}
	}
	return Outcome{
		Handled:          true,
		Prompt:           msg,
		ThinkDuration:    dur,
		HasThinkDuration: true,
	}
}

// /stream <duration> <message> — same as /think, but overrides the per-
// token stream interval rather than the spinner duration. Duration is
// required.
func (h *Handler) cmdStream(out io.Writer, rest string) Outcome {
	dur, msg, ok := parseDurationPrefix(rest)
	if !ok || msg == "" {
		fmt.Fprintln(out, "usage: /stream <duration> <message>")
		return Outcome{Handled: true}
	}
	return Outcome{
		Handled:           true,
		Prompt:            msg,
		StreamDuration:    dur,
		HasStreamDuration: true,
	}
}

// parseDurationPrefix splits rest into (duration, message). The first
// whitespace-delimited token must parse via time.ParseDuration (negative
// values clamp to zero). Returns ok=false if the prefix isn't a valid
// duration; callers render a usage line in that case.
//
// Examples:
//
//	"5s working on it" → {5s, "working on it", true}
//	"100ms hi"         → {100ms, "hi", true}
//	"5s"               → {5s, "", true}                   // empty msg
//	"working on it"    → {0, "", false}                   // no duration
//	""                 → {0, "", false}                   // empty
//	"-5s clamp"        → {0, "clamp", true}               // negative clamps
func parseDurationPrefix(rest string) (time.Duration, string, bool) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return 0, "", false
	}
	first, tail := splitFirstWord(rest)
	d, err := time.ParseDuration(first)
	if err != nil {
		return 0, "", false
	}
	if d < 0 {
		d = 0
	}
	return d, strings.TrimSpace(tail), true
}

// /panel <text> — rounded-border panel via lipgloss.
func (h *Handler) cmdPanel(out io.Writer, text string) {
	fmt.Fprintln(out, render.PanelStyle.Render(text))
}

// /link <url> [text] — OSC 8 hyperlink. The escape sequence is
// `\x1b]8;;<URL>\x1b\\<TEXT>\x1b]8;;\x1b\\` (start, params-empty, URL,
// ST, text, start, params-empty, ST). Most modern terminals (iTerm2,
// Ghostty, WezTerm, Kitty, GNOME Terminal, modern xterm, VS Code's
// integrated terminal) render the text as a clickable link. Terminals
// that don't support OSC 8 just print the text. No hooks fire — this
// is a pure UI primitive like /panel. Empty text falls back to the URL
// itself, matching the convention used by `gh`, `git`, etc.
func (h *Handler) cmdLink(out io.Writer, rest string) {
	url, text := splitFirstWord(rest)
	if url == "" {
		fmt.Fprintln(out, "usage: /link <url> [text]")
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = url
	}
	fmt.Fprintf(out, "\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\\n", url, text)
}

// /fake-tool <name> <json-args> — render the tool-use block and record the call
// as pending. The matching /fake-tool-result completes it and fires PostToolUse with
// the full payload (input + response + duration). Submitting another /fake-tool
// while one is pending flushes the prior with empty response.
//
// If PreToolUse returns deny, render a [blocked] marker and fire
// PostToolUse with an error tool_response (no pending kept). If it
// returns ask, render an [awaiting permission] marker and keep the
// pending entry with awaitingPermission=true.
func (h *Handler) cmdFakeTool(ctx context.Context, out io.Writer, rest string) {
	name, jsonArgs := splitFirstWord(rest)
	if name == "" {
		fmt.Fprintln(os.Stderr, "testagent: /fake-tool requires a tool name")
		return
	}
	args := parseJSONOr(jsonArgs, map[string]any{})
	prettyArgs, _ := json.Marshal(args)
	fmt.Fprintf(out, "%s %s\n",
		render.ToolHeader("▶ ", name),
		render.MuteStyle.Render(string(prettyArgs)))

	// Flush any prior pending /fake-tool that never got a /fake-tool-result.
	if prior := h.takePending(); prior != nil {
		fmt.Fprintf(os.Stderr, "testagent: /fake-tool replaced pending %q without /fake-tool-result\n", prior.name)
		h.firePendingHook(ctx, prior, nil)
	}
	toolUseID := "toolu_" + randomHex(12)
	startedAt := time.Now()
	result, err := h.hooks.OnPreToolUse(ctx, toolUseID, name, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnPreToolUse: %v\n", err)
	}
	switch {
	case result.Block:
		// Hook denied; render marker and emit PostToolUse with an error
		// response so orchestrators see the full lifecycle.
		marker := "blocked by hook"
		if result.Reason != "" {
			marker += ": " + result.Reason
		}
		fmt.Fprintf(out, "%s\n", render.LifecycleWarn(marker))
		toolResponse := map[string]any{"error": "blocked", "reason": result.Reason}
		dur := time.Since(startedAt).Milliseconds()
		if err := h.hooks.OnPostToolUse(ctx, toolUseID, name, args, toolResponse, dur); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnPostToolUse: %v\n", err)
		}
	case result.Ask:
		marker := "awaiting permission"
		if result.Reason != "" {
			marker += ": " + result.Reason
		}
		fmt.Fprintf(out, "%s\n", render.Lifecycle(marker))
		h.setPending(&pendingToolCall{
			toolUseID:          toolUseID,
			name:               name,
			input:              args,
			startedAt:          startedAt,
			awaitingPermission: true,
			permissionReason:   result.Reason,
		})
	default:
		h.setPending(&pendingToolCall{
			toolUseID: toolUseID,
			name:      name,
			input:     args,
			startedAt: startedAt,
		})
	}
}

// /fake-tool-result <json-or-text> — render the matching fake-tool result with a checkmark
// and fire PostToolUse with the captured /fake-tool input + this response +
// measured duration. JSON results are stored structured; non-JSON as the
// raw string. With no pending /fake-tool, only renders (no synthetic hook —
// inventing a tool_use_id and tool_name would produce dishonest fixtures).
//
// When the pending is in awaitingPermission state (PreToolUse returned
// ask), this short-circuits: a marker renders, the pending is restored,
// and PostToolUse does not fire.
func (h *Handler) cmdFakeToolResult(ctx context.Context, out io.Writer, rest string) {
	// Take pending atomically; if it's awaiting permission, restore and
	// short-circuit. Peeking under one lock then taking under another
	// races against a concurrent /fake-tool replacing the entry.
	pending := h.takePending()
	if pending != nil && pending.awaitingPermission {
		h.setPending(pending)
		fmt.Fprintf(out, "%s\n", render.Lifecycle("still awaiting permission — pending preserved"))
		return
	}
	mark := render.ResultOk()
	var response any
	switch {
	case rest == "":
		fmt.Fprintf(out, "%s %s\n", mark, render.MuteSoftStyle.Render("(empty result)"))
	default:
		var parsed any
		if err := json.Unmarshal([]byte(rest), &parsed); err == nil {
			pretty, _ := json.MarshalIndent(parsed, "  ", "  ")
			fmt.Fprintf(out, "%s\n  %s\n", mark, render.MuteSoftStyle.Render(string(pretty)))
			response = parsed
		} else {
			fmt.Fprintf(out, "%s %s\n", mark, rest)
			response = rest
		}
	}

	if pending != nil {
		h.firePendingHook(ctx, pending, response)
	}
}

// FlushPendingTool fires PostToolUse for any in-flight /fake-tool with a nil
// response. Called from shutdown paths (/exit, signal, EOF, auto-exit) so
// dangling /fake-tool calls don't silently lose their hook event.
//
// An awaitingPermission pending is flushed with a synthetic blocked
// response — the orchestrator never resolved the permission, so the
// equivalent terminal state is "denied by timeout / shutdown."
func (h *Handler) FlushPendingTool(ctx context.Context) {
	pending := h.takePending()
	if pending == nil {
		return
	}
	if pending.awaitingPermission {
		fmt.Fprintf(os.Stderr, "testagent: /fake-tool %q flushed on shutdown while awaiting permission\n", pending.name)
		dur := time.Since(pending.startedAt).Milliseconds()
		toolResponse := map[string]any{"error": "blocked", "reason": "shutdown before permission resolution"}
		if err := h.hooks.OnPostToolUse(ctx, pending.toolUseID, pending.name, pending.input, toolResponse, dur); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnPostToolUse: %v\n", err)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "testagent: /fake-tool %q flushed on shutdown without /fake-tool-result\n", pending.name)
	h.firePendingHook(ctx, pending, nil)
}

func (h *Handler) setPending(p *pendingToolCall) {
	h.pendingToolMu.Lock()
	defer h.pendingToolMu.Unlock()
	h.pendingTool = p
}

func (h *Handler) takePending() *pendingToolCall {
	h.pendingToolMu.Lock()
	defer h.pendingToolMu.Unlock()
	p := h.pendingTool
	h.pendingTool = nil
	return p
}

// firePendingHook posts PostToolUse for a captured /fake-tool. response is the
// /fake-tool-result body (parsed JSON or raw string) or nil when flushing a
// dangling /fake-tool. Errors are logged to stderr and do not abort the session.
func (h *Handler) firePendingHook(ctx context.Context, p *pendingToolCall, response any) {
	dur := time.Since(p.startedAt).Milliseconds()
	if err := h.hooks.OnPostToolUse(ctx, p.toolUseID, p.name, p.input, response, dur); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnPostToolUse: %v\n", err)
	}
}

// /mcp-call <qualified-tool> <json-args> — invoke a real connected MCP tool.
// qualified-tool is "<server>.<tool>". Named to avoid collision with real
// Claude Code's /mcp, which opens a server-management UI rather than calling
// a tool — orchestrators can pipe both verbatim and get distinct behavior.
func (h *Handler) cmdMCP(ctx context.Context, out io.Writer, rest string) {
	qualified, jsonArgs := splitFirstWord(rest)
	if qualified == "" || !strings.Contains(qualified, ".") {
		fmt.Fprintln(os.Stderr, "testagent: /mcp-call requires <server>.<tool> as first arg")
		return
	}
	args := parseJSONOr(jsonArgs, map[string]any{})

	fmt.Fprintf(out, "%s %s\n",
		render.ToolHeader("▶ mcp:", qualified),
		render.MuteStyle.Render(jsonArgs))

	res, err := h.mcp.Call(ctx, qualified, args)
	if err != nil {
		fmt.Fprintf(out, "%s %s %v\n", render.ResultErr(), render.ErrorStyle.Render("mcp error:"), err)
		return
	}
	mark := render.ResultOk()
	for _, c := range res.Content {
		if c.Type == "text" {
			fmt.Fprintf(out, "%s %s\n", mark, c.Text)
		} else {
			fmt.Fprintf(out, "%s %s\n", mark, render.MuteSoftStyle.Render("("+c.Type+" content)"))
		}
	}
}

// /clear — fire SessionEnd(reason=clear) → SessionStart(source=clear).
// The runtime owns the actual hook firing via the Outcome (parallel to
// /exit's outcome-driven shutdown).
func (h *Handler) cmdClear(out io.Writer) Outcome {
	fmt.Fprintln(out, render.Lifecycle("clear"))
	return Outcome{Handled: true, Restart: true, RestartReason: "clear"}
}

// /compact (trigger="manual") and /fake-auto-compact (trigger="auto") —
// fire PreCompact(trigger) → SessionEnd(compact) → SessionStart(compact)
// → PostCompact(trigger). Wiring lives in the engine; this just emits
// the outcome.
func (h *Handler) cmdCompact(out io.Writer, trigger string) Outcome {
	label := "compact"
	if trigger == "auto" {
		label = "compact (auto)"
	}
	fmt.Fprintln(out, render.Lifecycle(label))
	return Outcome{Handled: true, Restart: true, RestartReason: "compact", CompactTrigger: trigger}
}

// splitFirstWord splits on the first whitespace, returning (head, tail).
// Leading/trailing whitespace on tail is preserved beyond the single delim.
func splitFirstWord(s string) (string, string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

// parseJSONOr returns the parsed value for a JSON snippet, or fallback if
// the string is empty or invalid.
func parseJSONOr(s string, fallback any) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return fallback
	}
	return v
}

// randomHex returns 2*n hex characters for synthesizing tool-use IDs.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
