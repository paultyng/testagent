// Non-interactive (--print / -p) mode. Reads a single prompt from positional
// args (or stdin if none), produces an echo response, and emits it in the
// configured --output-format: text, json, or stream-json.
//
// Output shapes mirror Claude Code's --print formats so consumers parsing
// stream-json from a real claude binary can parse testagent's output the
// same way.
//
// --verbose hook trace lines (when enabled) go to stderr via the hook
// sender, so stdout stays clean for stream-json consumers reading JSONL
// frames.

package claude

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
)

// printOptions bundles the inputs runPrint needs from main().
type printOptions struct {
	name         string
	sessionID    string
	cwd          string
	model        string
	outputFormat string // "text" | "json" | "stream-json"
	positional   []string
	resumed      bool // true when --resume was set; selects SessionStart source
	hooks        *hooks.Sender
	mcp          *mcp.Client
}

// runPrint executes one non-interactive turn and returns the exit code.
//
// Lifecycle: SessionStart → read prompt → fire UserPromptSubmit hook →
// produce echo → emit per --output-format → fire Stop + SessionEnd →
// close MCP. Hook errors are logged to stderr and do not affect the exit
// code. SessionStart is paired with SessionEnd so orchestrators see a
// complete lifecycle even on one-shot invocations.
func runPrint(ctx context.Context, opt printOptions, stdin io.Reader, stdout io.Writer) int {
	startSource := "startup"
	if opt.resumed {
		startSource = "resume"
	}
	if err := opt.hooks.OnSessionStart(ctx, startSource); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnSessionStart: %v\n", err)
	}

	prompt := strings.TrimSpace(strings.Join(opt.positional, " "))
	if prompt == "" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "testagent: read prompt: %v\n", err)
			return 1
		}
		prompt = strings.TrimSpace(string(b))
	}
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "testagent: --print requires a prompt (positional arg or stdin)")
		return 1
	}

	if err := opt.hooks.OnPrompt(ctx, prompt, opt.name); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnPrompt: %v\n", err)
	}

	start := time.Now()
	response := fmt.Sprintf("[%s] %s", opt.name, prompt)
	durationMs := time.Since(start).Milliseconds()

	model := opt.model
	if model == "" {
		model = "testagent"
	}

	switch opt.outputFormat {
	case "json":
		emitJSON(stdout, opt.sessionID, response, durationMs, model)
	case "stream-json":
		emitStreamJSON(stdout, opt, response, durationMs, model)
	default: // "text" or unset
		fmt.Fprintln(stdout, response)
	}

	if err := opt.hooks.OnStop(ctx, response, false); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnStop: %v\n", err)
	}
	if err := opt.hooks.OnSessionEnd(ctx, "logout"); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnSessionEnd: %v\n", err)
	}
	if err := opt.mcp.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: mcp Close: %v\n", err)
	}
	return 0
}

// emitJSON writes a single JSON object summarizing the turn.
func emitJSON(w io.Writer, sessionID, result string, durationMs int64, model string) {
	out := map[string]any{
		"type":           "result",
		"subtype":        "success",
		"is_error":       false,
		"session_id":     sessionID,
		"result":         result,
		"duration_ms":    durationMs,
		"num_turns":      1,
		"model":          model,
		"total_cost_usd": 0.0,
		"usage": map[string]any{
			"input_tokens":  approxTokens(result),
			"output_tokens": approxTokens(result),
		},
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}

// emitStreamJSON writes the system-init / assistant-message / result frame
// sequence used by Claude Code's --output-format stream-json. One JSON
// object per line.
func emitStreamJSON(w io.Writer, opt printOptions, result string, durationMs int64, model string) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	// 1. system / init
	_ = enc.Encode(map[string]any{
		"type":           "system",
		"subtype":        "init",
		"session_id":     opt.sessionID,
		"cwd":            opt.cwd,
		"model":          model,
		"tools":          []string{},
		"mcp_servers":    mcpServersForInit(opt.mcp),
		"permissionMode": "default",
		"apiKeySource":   "none",
	})

	// 2. assistant message
	usage := map[string]any{
		"input_tokens":                approxTokens(result),
		"output_tokens":               approxTokens(result),
		"cache_read_input_tokens":     0,
		"cache_creation_input_tokens": 0,
		"service_tier":                "default",
	}
	_ = enc.Encode(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"id":            "msg_" + randomHex(12),
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []map[string]any{{"type": "text", "text": result}},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         usage,
		},
		"parent_tool_use_id": nil,
		"session_id":         opt.sessionID,
	})

	// 3. result
	_ = enc.Encode(map[string]any{
		"type":               "result",
		"subtype":            "success",
		"is_error":           false,
		"duration_ms":        durationMs,
		"duration_api_ms":    durationMs,
		"num_turns":          1,
		"result":             result,
		"session_id":         opt.sessionID,
		"total_cost_usd":     0.0,
		"usage":              usage,
		"permission_denials": []any{},
		"uuid":               newSessionID(),
	})
}

// mcpServersForInit returns the connected-server name list for the system/init
// event. Nil-safe: returns an empty slice when MCP isn't configured.
func mcpServersForInit(c *mcp.Client) []map[string]any {
	if c == nil {
		return []map[string]any{}
	}
	tools := c.Tools()
	seen := make(map[string]bool, len(tools))
	out := make([]map[string]any, 0)
	for _, t := range tools {
		if seen[t.Server] {
			continue
		}
		seen[t.Server] = true
		out = append(out, map[string]any{"name": t.Server, "status": "connected"})
	}
	return out
}

// approxTokens is a rough token estimate (~4 chars/token) for usage stats.
// testagent doesn't run a real tokenizer; this is fixture-quality only.
func approxTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	n := len(s) / 4
	if n == 0 {
		return 1
	}
	return n
}

// randomHex returns 2*n hex characters for synthesizing message IDs.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
