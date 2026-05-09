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

	"github.com/charmbracelet/lipgloss"
)

var (
	stylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("241")).
			Padding(0, 1)

	styleToolHeader = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true)

	styleToolArgs = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	styleResultMark = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)

	styleResultBody = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	styleThink = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)

	styleErr = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true)
)

// SlashHandler dispatches slash commands and renders their output.
type SlashHandler struct {
	name           string
	streamDelay    time.Duration
	sessionID      string
	cwd            string
	transcriptPath string
	permissionMode string
	hooks          *HookSender
	mcp            *MCPClient
	out            io.Writer

	// pendingToolMu guards pendingTool. Slash dispatches can run concurrently
	// in TUI mode (cmdSlashDispatch is a tea.Cmd goroutine), so the
	// /tool→/result pairing state needs synchronization.
	pendingToolMu sync.Mutex
	pendingTool   *pendingToolCall
}

// pendingToolCall tracks a /tool that hasn't been completed by /result yet.
// /result fires the PostToolUse hook with the captured tool_input plus the
// supplied tool_response and the measured duration. /tool followed by /tool
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
		h.cmdThink(out, rest)
	case "panel":
		h.cmdPanel(out, rest)
	case "tool":
		h.cmdTool(ctx, out, rest)
	case "result":
		h.cmdResult(ctx, out, rest)
	case "mcp":
		h.cmdMCP(ctx, out, rest)
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
	header := styleToolHeader.Render("slash commands")
	fmt.Fprintln(out, header)
	for _, line := range []struct {
		usage, doc string
	}{
		{"/exit [code]", "exits testagent (default code 0)"},
		{"/help", "prints this list"},
		{`/mcp <server.tool> <json-args>`, "calls a connected MCP tool and prints its result"},
		{"/panel <text>", "prints text in a rounded-border box"},
		{`/result <json-or-text>`, "completes the pending /tool and fires PostToolUse with the response"},
		{"/stream <text>", "prints text token-by-token at the configured pacing"},
		{`/think [<duration>] <text>`, "prints a dim italic thought; if duration parses, sleeps that long after"},
		{`/tool <name> <json-args>`, "prints a synthetic tool-use block; pair with /result to fire PostToolUse"},
	} {
		fmt.Fprintf(out, "  %-40s %s\n",
			styleToolArgs.Render(line.usage),
			styleResultBody.Render(line.doc))
	}
	fmt.Fprintln(out, styleResultBody.Render("input not starting with / is echoed back."))
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

// /think [<duration>] <text> — dim italic "thinking" trace. If the first
// whitespace-delimited token parses via time.ParseDuration, it's consumed
// as a sleep override (the message is rendered first, then the sleep runs)
// so demos can pin a thought to the screen for a known duration. Negative
// durations clamp to zero.
func (h *SlashHandler) cmdThink(out io.Writer, rest string) {
	d, msg := parseThinkArgs(rest)
	if msg == "" && d == 0 {
		return
	}
	if msg != "" {
		fmt.Fprintln(out, styleThink.Render(msg))
	}
	if d > 0 {
		time.Sleep(d)
	}
}

// parseThinkArgs splits rest into (duration, message). If the first token
// parses via time.ParseDuration, it's the duration and the remainder is the
// message. Otherwise duration is zero and rest is the full message.
// Negative durations are clamped to zero.
func parseThinkArgs(rest string) (time.Duration, string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return 0, ""
	}
	first, tail := splitFirstWord(rest)
	if d, err := time.ParseDuration(first); err == nil {
		if d < 0 {
			d = 0
		}
		return d, strings.TrimSpace(tail)
	}
	return 0, rest
}

// /panel <text> — rounded-border panel via lipgloss.
func (h *SlashHandler) cmdPanel(out io.Writer, text string) {
	fmt.Fprintln(out, stylePanel.Render(text))
}

// /tool <name> <json-args> — render the tool-use block and record the call
// as pending. The matching /result completes it and fires PostToolUse with
// the full payload (input + response + duration). Submitting another /tool
// while one is pending flushes the prior with empty response.
func (h *SlashHandler) cmdTool(ctx context.Context, out io.Writer, rest string) {
	name, jsonArgs := splitFirstWord(rest)
	if name == "" {
		fmt.Fprintln(os.Stderr, "testagent: /tool requires a tool name")
		return
	}
	args := parseJSONOr(jsonArgs, map[string]any{})
	prettyArgs, _ := json.Marshal(args)
	fmt.Fprintf(out, "%s %s\n",
		styleToolHeader.Render("▶ "+name),
		styleToolArgs.Render(string(prettyArgs)))

	// Flush any prior pending /tool that never got a /result.
	if prior := h.takePending(); prior != nil {
		fmt.Fprintf(os.Stderr, "testagent: /tool replaced pending %q without /result\n", prior.name)
		h.firePendingHook(ctx, prior, nil)
	}
	h.setPending(&pendingToolCall{
		toolUseID: "toolu_" + randomHex(12),
		name:      name,
		input:     args,
		startedAt: time.Now(),
	})
}

// /result <json-or-text> — render the matching tool result with a checkmark
// and fire PostToolUse with the captured /tool input + this response +
// measured duration. JSON results are stored structured; non-JSON as the
// raw string. With no pending /tool, only renders (no synthetic hook —
// inventing a tool_use_id and tool_name would produce dishonest fixtures).
func (h *SlashHandler) cmdResult(ctx context.Context, out io.Writer, rest string) {
	mark := styleResultMark.Render("✓")
	var response any
	switch {
	case rest == "":
		fmt.Fprintf(out, "%s %s\n", mark, styleResultBody.Render("(empty result)"))
	default:
		var parsed any
		if err := json.Unmarshal([]byte(rest), &parsed); err == nil {
			pretty, _ := json.MarshalIndent(parsed, "  ", "  ")
			fmt.Fprintf(out, "%s\n  %s\n", mark, styleResultBody.Render(string(pretty)))
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

// FlushPendingTool fires PostToolUse for any in-flight /tool with a nil
// response. Called from shutdown paths (/exit, signal, EOF, auto-exit) so
// dangling /tool calls don't silently lose their hook event.
func (h *SlashHandler) FlushPendingTool(ctx context.Context) {
	if pending := h.takePending(); pending != nil {
		fmt.Fprintf(os.Stderr, "testagent: /tool %q flushed on shutdown without /result\n", pending.name)
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

// firePendingHook posts PostToolUse for a captured /tool. response is the
// /result body (parsed JSON or raw string) or nil when flushing a dangling
// /tool. Errors are logged to stderr and do not abort the session.
func (h *SlashHandler) firePendingHook(ctx context.Context, p *pendingToolCall, response any) {
	dur := time.Since(p.startedAt).Milliseconds()
	if err := h.hooks.OnToolUse(ctx, p.toolUseID, p.name, p.input, response, dur); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnToolUse: %v\n", err)
	}
}

// /mcp <qualified-tool> <json-args> — invoke a real connected MCP tool.
// qualified-tool is "<server>.<tool>".
func (h *SlashHandler) cmdMCP(ctx context.Context, out io.Writer, rest string) {
	qualified, jsonArgs := splitFirstWord(rest)
	if qualified == "" || !strings.Contains(qualified, ".") {
		fmt.Fprintln(os.Stderr, "testagent: /mcp requires <server>.<tool> as first arg")
		return
	}
	args := parseJSONOr(jsonArgs, map[string]any{})

	fmt.Fprintf(out, "%s %s\n",
		styleToolHeader.Render("▶ mcp:"+qualified),
		styleToolArgs.Render(jsonArgs))

	res, err := h.mcp.Call(ctx, qualified, args)
	if err != nil {
		fmt.Fprintf(out, "%s %v\n", styleErr.Render("✗ mcp error:"), err)
		return
	}
	mark := styleResultMark.Render("✓")
	for _, c := range res.Content {
		if c.Type == "text" {
			fmt.Fprintf(out, "%s %s\n", mark, c.Text)
		} else {
			fmt.Fprintf(out, "%s %s\n", mark, styleResultBody.Render("("+c.Type+" content)"))
		}
	}
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
