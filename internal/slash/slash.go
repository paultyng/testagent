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

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/render"
)

// Handler dispatches slash commands and renders their output.
type Handler struct {
	hooks *hooks.Sender
	mcp   *mcp.Client
	out   io.Writer

	// pendingToolMu guards pendingTool. Slash dispatches can run concurrently
	// in TUI mode (cmdSlashDispatch is a tea.Cmd goroutine), so the
	// /fake-tool→/fake-tool-result pairing state needs synchronization.
	pendingToolMu sync.Mutex
	pendingTool   *pendingToolCall
}

// New returns a Handler wired with the supplied dependencies.
func New(sender *hooks.Sender, client *mcp.Client, out io.Writer) *Handler {
	return &Handler{
		hooks: sender,
		mcp:   client,
		out:   out,
	}
}

// pendingToolCall tracks a /fake-tool that has not been completed by /fake-tool-result yet.
// /fake-tool-result fires the PostToolUse hook with the captured tool_input plus the
// supplied tool_response and the measured duration. /fake-tool followed by /fake-tool
// flushes the prior with empty response; shutdown flushes whatever's left.
type pendingToolCall struct {
	toolUseID string
	name      string
	input     any
	startedAt time.Time
}

// Outcome reports control-flow signals from a slash command.
type Outcome struct {
	Handled  bool // input started with "/" and was dispatched
	Exit     bool // session should end (only set by /exit)
	ExitCode int  // exit status when Exit is true
	Reason   string

	// Restart, when true, signals the caller to fire SessionEnd then
	// SessionStart back-to-back without leaving the process. Set by /restart.
	// RestartReason is the matcher value passed through to both events
	// (SessionEnd reason / SessionStart source) — typically "clear" or
	// "compact" so an orchestrator can simulate either reset flavor.
	Restart       bool
	RestartReason string

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
// output as a string (so the model can append it to history) and returns it
// alongside the control-flow outcome. Concurrent-safe: each call writes to
// its own buffer, so multiple in-flight goroutines never interfere.
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
	case "fake-tool":
		h.cmdFakeTool(ctx, out, rest)
	case "fake-tool-result":
		h.cmdFakeToolResult(ctx, out, rest)
	case "mcp-call":
		h.cmdMCP(ctx, out, rest)
	case "restart":
		return h.cmdRestart(out, rest)
	case "exit":
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
		{"/exit [code]", "exits testagent (default code 0)"},
		{`/fake-tool <name> <json-args>`, "prints a fake tool-use block; pair with /fake-tool-result to fire PostToolUse"},
		{`/fake-tool-result <json-or-text>`, "completes the pending /fake-tool and fires PostToolUse with the response"},
		{"/help", "prints this list"},
		{`/mcp-call <server.tool> <json-args>`, "calls a connected MCP tool and prints its result"},
		{"/panel <text>", "prints text in a rounded-border box"},
		{"/restart [clear|compact]", "fires SessionEnd then SessionStart without leaving the process (default reason: clear)"},
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

// /fake-tool <name> <json-args> — render the tool-use block and record the call
// as pending. The matching /fake-tool-result completes it and fires PostToolUse with
// the full payload (input + response + duration). Submitting another /fake-tool
// while one is pending flushes the prior with empty response.
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
	h.setPending(&pendingToolCall{
		toolUseID: "toolu_" + randomHex(12),
		name:      name,
		input:     args,
		startedAt: time.Now(),
	})
}

// /fake-tool-result <json-or-text> — render the matching fake-tool result with a checkmark
// and fire PostToolUse with the captured /fake-tool input + this response +
// measured duration. JSON results are stored structured; non-JSON as the
// raw string. With no pending /fake-tool, only renders (no synthetic hook —
// inventing a tool_use_id and tool_name would produce dishonest fixtures).
func (h *Handler) cmdFakeToolResult(ctx context.Context, out io.Writer, rest string) {
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

	if pending := h.takePending(); pending != nil {
		h.firePendingHook(ctx, pending, response)
	}
}

// FlushPendingTool fires PostToolUse for any in-flight /fake-tool with a nil
// response. Called from shutdown paths (/exit, signal, EOF, auto-exit) so
// dangling /fake-tool calls don't silently lose their hook event.
func (h *Handler) FlushPendingTool(ctx context.Context) {
	if pending := h.takePending(); pending != nil {
		fmt.Fprintf(os.Stderr, "testagent: /fake-tool %q flushed on shutdown without /fake-tool-result\n", pending.name)
		h.firePendingHook(ctx, pending, nil)
	}
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
	if err := h.hooks.OnToolUse(ctx, p.toolUseID, p.name, p.input, response, dur); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnToolUse: %v\n", err)
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

// /restart [clear|compact] — emit SessionEnd + SessionStart without leaving
// the process. The shared matcher value (default "clear") is passed as
// SessionEnd.reason and SessionStart.source so an orchestrator can simulate
// either Claude reset flavor — `/clear`-style ("clear") or `/compact`-style
// ("compact"). The runtime owns the actual hook firing via the Outcome
// (parallel to /exit's outcome-driven shutdown).
func (h *Handler) cmdRestart(out io.Writer, rest string) Outcome {
	reason := strings.TrimSpace(rest)
	if reason == "" {
		reason = "clear"
	}
	fmt.Fprintln(out, render.Lifecycle("restart: "+reason))
	return Outcome{Handled: true, Restart: true, RestartReason: reason}
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
