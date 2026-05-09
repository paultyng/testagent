// Slash-command grammar for driving UI primitives interactively.
//
// During an interactive session, lines starting with "/" are interpreted as
// directives that synthesize specific UI elements (streamed text, tool-use
// blocks, panels, MCP calls) instead of going through the default echo path.

package main

import (
	"bytes"
	"context"
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

// SlashHandler dispatches slash commands and renders their output.
type SlashHandler struct {
	name           string
	streamDelay    time.Duration
	sessionID      string
	cwd            string
	transcriptPath string
	permissionMode string
	hooks          *hooks.Sender
	mcp            *mcp.Client
	out            io.Writer

	// pendingToolMu guards pendingTool. Slash dispatches can run concurrently
	// in TUI mode (cmdSlashDispatch is a tea.Cmd goroutine), so the
	// /fake-tool→/fake-tool-result pairing state needs synchronization.
	pendingToolMu sync.Mutex
	pendingTool   *pendingToolCall
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

// SlashOutcome reports control-flow signals from a slash command.
type SlashOutcome struct {
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
	// thinking animation → echo → Stop hook). Set by /think after parsing
	// the optional leading duration. Without this signal, /think and raw
	// input would diverge — they're meant to be functionally identical.
	Prompt string

	// ThinkDuration overrides the caller's default --delay for this turn.
	// Zero means "use the caller's default." HasThinkDuration distinguishes
	// "no duration parsed, use default" from "explicit /think 0 done"
	// (immediate echo, zero-thinking).
	ThinkDuration    time.Duration
	HasThinkDuration bool
}

// DispatchString is the TUI-friendly entry point. It captures all rendered
// output as a string (so the model can append it to history) and returns it
// alongside the control-flow outcome. Concurrent-safe: each call writes to
// its own buffer, so multiple in-flight goroutines never interfere.
func (h *SlashHandler) DispatchString(ctx context.Context, line string) (string, SlashOutcome) {
	var buf bytes.Buffer
	outcome := h.dispatchTo(ctx, line, &buf)
	return buf.String(), outcome
}

// Dispatch parses and runs a single line, writing rendered output to h.out.
// If line doesn't start with "/", returns Handled=false. Errors go to stderr.
func (h *SlashHandler) Dispatch(ctx context.Context, line string) SlashOutcome {
	return h.dispatchTo(ctx, line, h.out)
}

// dispatchTo is the shared dispatch core. All cmd* methods write to the
// passed-in writer rather than h.out so callers can safely run concurrent
// dispatches without sharing per-handler state.
func (h *SlashHandler) dispatchTo(ctx context.Context, line string, out io.Writer) SlashOutcome {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return SlashOutcome{}
	}
	cmd, rest := splitFirstWord(line[1:])
	rest = strings.TrimSpace(rest)

	switch cmd {
	case "help", "?":
		h.cmdHelp(out)
	case "stream":
		h.cmdStream(out, rest)
	case "think":
		return h.cmdThink(rest)
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
		return SlashOutcome{Handled: true, Exit: true, ExitCode: code, Reason: "logout"}
	default:
		fmt.Fprintf(os.Stderr, "testagent: unknown slash command %q (try /help)\n", "/"+cmd)
	}
	return SlashOutcome{Handled: true}
}

// /help — list slash commands with their argument signatures.
func (h *SlashHandler) cmdHelp(out io.Writer) {
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
		{"/stream <text>", "prints text token-by-token at the configured pacing"},
		{`/think [<duration>] <text>`, "prompts as if typed raw; the optional duration overrides the default thinking time (3s)"},
	} {
		fmt.Fprintf(out, "  %-40s %s\n",
			render.MuteStyle.Render(line.usage),
			render.MuteSoftStyle.Render(line.doc))
	}
	fmt.Fprintln(out, render.MuteSoftStyle.Render("input not starting with / is echoed back."))
}

// /stream <text> — token-paced streaming text.
func (h *SlashHandler) cmdStream(out io.Writer, text string) {
	if text == "" {
		fmt.Fprintln(out)
		return
	}
	tokens := strings.Fields(text)
	for i, t := range tokens {
		if i > 0 {
			fmt.Fprint(out, " ")
		}
		fmt.Fprint(out, t)
		if h.streamDelay > 0 {
			time.Sleep(h.streamDelay)
		}
	}
	fmt.Fprintln(out)
}

// /think [<duration>] <text> — functionally identical to typing <text> as
// raw input, with the addition of an optional leading time.Duration that
// overrides the default thinking time for that turn. Returns a SlashOutcome
// with Prompt and (optionally) ThinkDuration set; the dispatcher routes
// the message back through the prompt-handling path so hooks fire and the
// thinking animation runs.
func (h *SlashHandler) cmdThink(rest string) SlashOutcome {
	req := parseThinkArgs(rest)
	return SlashOutcome{
		Handled:          true,
		Prompt:           req.Message,
		ThinkDuration:    req.Duration,
		HasThinkDuration: req.HasExplicit,
	}
}

// ThinkRequest is the parsed form of /think arguments.
type ThinkRequest struct {
	Duration    time.Duration // explicit duration when HasExplicit is true; otherwise 0
	HasExplicit bool          // true iff the first token parsed via time.ParseDuration
	Message     string        // the message to prompt with (may be empty)
}

// parseThinkArgs splits rest into a ThinkRequest. If the first whitespace-
// delimited token parses via time.ParseDuration, it's the duration (negative
// values clamp to zero) and the remainder is the message. Otherwise the full
// rest is the message and HasExplicit is false; callers substitute their
// default thinking duration.
//
// Examples:
//
//	"5s working on it" → {5s, true, "working on it"}
//	"working on it"    → {0, false, "working on it"}      // caller uses default
//	"5seconds working" → {0, false, "5seconds working"}   // first token rejected
//	"5s"               → {5s, true, ""}                   // duration only
//	""                 → {0, false, ""}                   // bare /think
//	"0 done"           → {0, true, "done"}                // explicit zero (instant)
//	"-5s clamp"        → {0, true, "clamp"}               // negative clamps
func parseThinkArgs(rest string) ThinkRequest {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ThinkRequest{}
	}
	first, tail := splitFirstWord(rest)
	if d, err := time.ParseDuration(first); err == nil {
		if d < 0 {
			d = 0
		}
		return ThinkRequest{Duration: d, HasExplicit: true, Message: strings.TrimSpace(tail)}
	}
	return ThinkRequest{Message: rest}
}

// /panel <text> — rounded-border panel via lipgloss.
func (h *SlashHandler) cmdPanel(out io.Writer, text string) {
	fmt.Fprintln(out, render.PanelStyle.Render(text))
}

// /fake-tool <name> <json-args> — render the tool-use block and record the call
// as pending. The matching /fake-tool-result completes it and fires PostToolUse with
// the full payload (input + response + duration). Submitting another /fake-tool
// while one is pending flushes the prior with empty response.
func (h *SlashHandler) cmdFakeTool(ctx context.Context, out io.Writer, rest string) {
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
func (h *SlashHandler) cmdFakeToolResult(ctx context.Context, out io.Writer, rest string) {
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
func (h *SlashHandler) FlushPendingTool(ctx context.Context) {
	if pending := h.takePending(); pending != nil {
		fmt.Fprintf(os.Stderr, "testagent: /fake-tool %q flushed on shutdown without /fake-tool-result\n", pending.name)
		h.firePendingHook(ctx, pending, nil)
	}
}

func (h *SlashHandler) setPending(p *pendingToolCall) {
	h.pendingToolMu.Lock()
	defer h.pendingToolMu.Unlock()
	h.pendingTool = p
}

func (h *SlashHandler) takePending() *pendingToolCall {
	h.pendingToolMu.Lock()
	defer h.pendingToolMu.Unlock()
	p := h.pendingTool
	h.pendingTool = nil
	return p
}

// firePendingHook posts PostToolUse for a captured /fake-tool. response is the
// /fake-tool-result body (parsed JSON or raw string) or nil when flushing a
// dangling /fake-tool. Errors are logged to stderr and do not abort the session.
func (h *SlashHandler) firePendingHook(ctx context.Context, p *pendingToolCall, response any) {
	dur := time.Since(p.startedAt).Milliseconds()
	if err := h.hooks.OnToolUse(ctx, p.toolUseID, p.name, p.input, response, dur); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnToolUse: %v\n", err)
	}
}

// /mcp-call <qualified-tool> <json-args> — invoke a real connected MCP tool.
// qualified-tool is "<server>.<tool>". Named to avoid collision with real
// Claude Code's /mcp, which opens a server-management UI rather than calling
// a tool — orchestrators can pipe both verbatim and get distinct behavior.
func (h *SlashHandler) cmdMCP(ctx context.Context, out io.Writer, rest string) {
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
// ("compact"). The runtime owns the actual hook firing via the SlashOutcome
// (parallel to /exit's outcome-driven shutdown).
func (h *SlashHandler) cmdRestart(out io.Writer, rest string) SlashOutcome {
	reason := strings.TrimSpace(rest)
	if reason == "" {
		reason = "clear"
	}
	fmt.Fprintln(out, render.Lifecycle("restart: "+reason))
	return SlashOutcome{Handled: true, Restart: true, RestartReason: reason}
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
