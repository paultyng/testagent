// Slash-command grammar for driving UI primitives interactively.
//
// During an interactive session, lines starting with "/" are interpreted as
// directives that synthesize specific UI elements (streamed text, tool-use
// blocks, panels, markdown, MCP calls) instead of going through the default
// echo path. Same engine drives non-slash input in phase 2f's renderer.

package main

import (
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

// Dispatch parses and runs a single line. If line doesn't start with "/",
// returns Handled=false. All rendering goes to h.out; errors go to stderr.
func (h *SlashHandler) Dispatch(ctx context.Context, line string) SlashOutcome {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return SlashOutcome{}
	}
	cmd, rest := splitFirstWord(line[1:])
	rest = strings.TrimSpace(rest)

	switch cmd {
	case "help", "?":
		h.cmdHelp()
	case "stream":
		h.cmdStream(rest)
	case "think":
		h.cmdThink(rest)
	case "md":
		h.cmdMarkdown(rest)
	case "panel":
		h.cmdPanel(rest)
	case "tool":
		h.cmdTool(ctx, rest)
	case "result":
		h.cmdResult(rest)
	case "mcp":
		h.cmdMCP(ctx, rest)
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
func (h *SlashHandler) cmdHelp() {
	header := styleToolHeader.Render("slash commands")
	fmt.Fprintln(h.out, header)
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
		fmt.Fprintf(h.out, "  %-40s %s\n",
			styleToolArgs.Render(line.usage),
			styleResultBody.Render(line.doc))
	}
	fmt.Fprintln(h.out, styleResultBody.Render("input not starting with / is echoed back."))
}

// /stream <text> — token-paced streaming text.
func (h *SlashHandler) cmdStream(text string) {
	if text == "" {
		fmt.Fprintln(h.out)
		return
	}
	tokens := strings.Fields(text)
	for i, t := range tokens {
		if i > 0 {
			fmt.Fprint(h.out, " ")
		}
		fmt.Fprint(h.out, t)
		if h.streamDelay > 0 {
			time.Sleep(h.streamDelay)
		}
	}
	fmt.Fprintln(h.out)
}

// /think <text> — dim italic "thinking" trace, not part of the visible response.
func (h *SlashHandler) cmdThink(text string) {
	if text == "" {
		return
	}
	fmt.Fprintln(h.out, styleThink.Render(text))
}

// /md <markdown> — render via glamour (auto-detects terminal capabilities).
// Falls back to plain text on render failure.
func (h *SlashHandler) cmdMarkdown(md string) {
	rendered, err := glamour.Render(md, "auto")
	if err != nil {
		fmt.Fprintln(h.out, md)
		return
	}
	fmt.Fprint(h.out, rendered)
}

// /panel <text> — rounded-border panel via lipgloss.
func (h *SlashHandler) cmdPanel(text string) {
	fmt.Fprintln(h.out, stylePanel.Render(text))
}

// /tool <name> <json-args> — tool-use block (header + indented args) and
// fires the PostToolUse hook. The block prints; the matching /result completes it.
func (h *SlashHandler) cmdTool(ctx context.Context, rest string) {
	name, jsonArgs := splitFirstWord(rest)
	if name == "" {
		fmt.Fprintln(os.Stderr, "testagent: /tool requires a tool name")
		return
	}
	args := parseJSONOr(jsonArgs, map[string]any{})
	prettyArgs, _ := json.Marshal(args)
	fmt.Fprintf(h.out, "%s %s\n",
		styleToolHeader.Render("▶ "+name),
		styleToolArgs.Render(string(prettyArgs)))

	toolUseID := "toolu_" + randomHex(12)
	if err := h.hooks.OnToolUse(ctx, toolUseID, name, args, "", 0); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnToolUse: %v\n", err)
	}
}

// /result <json-or-text> — render the matching tool result with a checkmark.
// JSON is pretty-printed; raw text passes through.
func (h *SlashHandler) cmdResult(rest string) {
	mark := styleResultMark.Render("✓")
	if rest == "" {
		fmt.Fprintf(h.out, "%s %s\n", mark, styleResultBody.Render("(empty result)"))
		return
	}
	var parsed any
	if err := json.Unmarshal([]byte(rest), &parsed); err == nil {
		pretty, _ := json.MarshalIndent(parsed, "  ", "  ")
		fmt.Fprintf(h.out, "%s\n  %s\n", mark, styleResultBody.Render(string(pretty)))
		return
	}
	fmt.Fprintf(h.out, "%s %s\n", mark, rest)
}

// /mcp <qualified-tool> <json-args> — invoke a real connected MCP tool.
// qualified-tool is "<server>.<tool>".
func (h *SlashHandler) cmdMCP(ctx context.Context, rest string) {
	qualified, jsonArgs := splitFirstWord(rest)
	if qualified == "" || !strings.Contains(qualified, ".") {
		fmt.Fprintln(os.Stderr, "testagent: /mcp requires <server>.<tool> as first arg")
		return
	}
	args := parseJSONOr(jsonArgs, map[string]any{})

	fmt.Fprintf(h.out, "%s %s\n",
		styleToolHeader.Render("▶ mcp:"+qualified),
		styleToolArgs.Render(jsonArgs))

	res, err := h.mcp.Call(ctx, qualified, args)
	if err != nil {
		fmt.Fprintf(h.out, "%s %v\n", styleErr.Render("✗ mcp error:"), err)
		return
	}
	mark := styleResultMark.Render("✓")
	for _, c := range res.Content {
		if c.Type == "text" {
			fmt.Fprintf(h.out, "%s %s\n", mark, c.Text)
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
