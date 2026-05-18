// Non-interactive (--print) mode for cursor. Reads a single prompt from
// positional args (or stdin if none), produces an echo response, and emits
// it in the configured --output-format: text, json, or stream-json.
//
// Output shapes mirror cursor agent's --print formats per
// cursor.com/docs/cli/reference/output-format so consumers parsing
// stream-json from a real cursor binary can parse testagent's output the
// same way. Cursor's stream-json frame set is distinct from claude's:
// no usage tokens, no model_call_id by default, and tool_call frames use
// typed variants (readToolCall, writeToolCall, shellToolCall, editToolCall,
// or a function fallback) rather than claude's flat tool_use shape.
//
// Tool-call frames are not emitted in Phase 4 — testagent's --print echo
// path doesn't dispatch tools. The system/init → user → assistant → result
// sequence is complete on its own; future phases may add tool emissions
// when /fake-tool can drive the print path.

package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/paultyng/testagent/internal/cursorhooks"
	"github.com/paultyng/testagent/internal/mcp"
)

// printOptions bundles the inputs runPrint needs from the cursor RunE.
type printOptions struct {
	name         string
	sessionID    string
	cwd          string
	model        string
	outputFormat string // "text" | "json" | "stream-json"
	positional   []string
	resumed      bool
	hooks        *cursorhooks.Runner
	mcp          *mcp.Client
}

// runPrint executes one non-interactive turn and returns the exit code.
//
// Lifecycle (cursor): OnSessionStart (no-op — cursor has no event) → read
// prompt → OnPrompt (no-op for the same reason) → echo response → emit per
// --output-format → OnStop ("stop" event fires) → OnSessionEnd (no-op) →
// close MCP. Hook errors land on stderr and don't affect the exit code.
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
		emitStreamJSON(stdout, opt, prompt, response, durationMs, model)
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

// emitJSON writes a single JSON object summarizing the turn. Cursor's
// --output-format json is not explicitly documented in upstream specs;
// this shape mirrors the result frame from stream-json so parsers can
// reuse the same field map.
func emitJSON(w io.Writer, sessionID, result string, durationMs int64, model string) {
	out := map[string]any{
		"type":            "result",
		"subtype":         "success",
		"is_error":        false,
		"session_id":      sessionID,
		"result":          result,
		"duration_ms":     durationMs,
		"duration_api_ms": durationMs,
		"model":           model,
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
}

// emitStreamJSON writes the cursor stream-json frame sequence: system/init,
// user, assistant, result. One JSON object per line. Distinct from claude's
// shape: no usage tokens, no tools / mcp_servers / apiKeySource enumeration
// beyond a "none" string, no parent_tool_use_id, no permission_denials. The
// frame set matches cursor.com/docs/cli/reference/output-format as of
// Cursor CLI 3.2.x.
func emitStreamJSON(w io.Writer, opt printOptions, prompt, result string, durationMs int64, model string) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	// 1. system / init
	_ = enc.Encode(map[string]any{
		"type":           "system",
		"subtype":        "init",
		"apiKeySource":   "none",
		"cwd":            opt.cwd,
		"session_id":     opt.sessionID,
		"model":          model,
		"permissionMode": "default",
	})

	// 2. user
	_ = enc.Encode(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": prompt}},
		},
		"session_id": opt.sessionID,
	})

	// 3. assistant
	_ = enc.Encode(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]any{{"type": "text", "text": result}},
		},
		"session_id": opt.sessionID,
	})

	// 4. result
	_ = enc.Encode(map[string]any{
		"type":            "result",
		"subtype":         "success",
		"is_error":        false,
		"duration_ms":     durationMs,
		"duration_api_ms": durationMs,
		"result":          result,
		"session_id":      opt.sessionID,
	})
}

