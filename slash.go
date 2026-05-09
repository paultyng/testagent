// Slash-command grammar for driving UI primitives interactively.
//
// During an interactive session, lines starting with "/" are interpreted as
// directives that synthesize specific UI elements (streamed text, tool-use
// blocks, panels, markdown, MCP calls) instead of going through the default
// echo path.

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
	"time"

	"github.com/charmbracelet/glamour"
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
	case "md":
		h.cmdMarkdown(out, rest)
	case "panel":
		h.cmdPanel(out, rest)
	case "tool":
		h.cmdTool(ctx, out, rest)
	case "result":
		h.cmdResult(out, rest)
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
		{"/exit [code]", "end the session (default code 0)"},
		{"/help", "show this list"},
		{`/mcp <server.tool> <json-args>`, "invoke a connected MCP tool"},
		{"/md <markdown>", "render markdown via glamour"},
		{"/panel <text>", "rounded-border panel"},
		{`/result <json-or-text>`, "complete the matching tool block"},
		{"/stream <text>", "token-paced streaming text"},
		{"/think <text>", "dim italic 'thinking' trace"},
		{`/tool <name> <json-args>`, "tool-use block + fires PostToolUse hook"},
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

// /think <text> — dim italic "thinking" trace, not part of the visible response.
func (h *SlashHandler) cmdThink(out io.Writer, text string) {
	if text == "" {
		return
	}
	fmt.Fprintln(out, styleThink.Render(text))
}

// /md <markdown> — render via glamour (auto-detects terminal capabilities).
// Falls back to plain text on render failure.
func (h *SlashHandler) cmdMarkdown(out io.Writer, md string) {
	rendered, err := glamour.Render(md, "auto")
	if err != nil {
		fmt.Fprintln(out, md)
		return
	}
	fmt.Fprint(out, rendered)
}

// /panel <text> — rounded-border panel via lipgloss.
func (h *SlashHandler) cmdPanel(out io.Writer, text string) {
	fmt.Fprintln(out, stylePanel.Render(text))
}

// /tool <name> <json-args> — tool-use block (header + indented args) and
// fires the PostToolUse hook. The block prints; the matching /result completes it.
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

	toolUseID := "toolu_" + randomHex(12)
	if err := h.hooks.OnToolUse(ctx, toolUseID, name, args, "", 0); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnToolUse: %v\n", err)
	}
}

// /result <json-or-text> — render the matching tool result with a checkmark.
// JSON is pretty-printed; raw text passes through.
func (h *SlashHandler) cmdResult(out io.Writer, rest string) {
	mark := styleResultMark.Render("✓")
	if rest == "" {
		fmt.Fprintf(out, "%s %s\n", mark, styleResultBody.Render("(empty result)"))
		return
	}
	var parsed any
	if err := json.Unmarshal([]byte(rest), &parsed); err == nil {
		pretty, _ := json.MarshalIndent(parsed, "  ", "  ")
		fmt.Fprintf(out, "%s\n  %s\n", mark, styleResultBody.Render(string(pretty)))
		return
	}
	fmt.Fprintf(out, "%s %s\n", mark, rest)
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
			fmt.Fprintf(h.out, "%s %s\n", mark, styleResultBody.Render("("+c.Type+" content)"))
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
