package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/paultyng/testagent/internal/cursorhooks"
	"github.com/paultyng/testagent/internal/mcp"
)

// newNoopHooks returns a cursorhooks.Runner with no matchers — the print
// path still calls its lifecycle methods but they do nothing.
func newNoopHooks() *cursorhooks.Runner {
	return cursorhooks.NewRunner(nil, "sid-test", "/tmp", "", "default", nil)
}

func TestRunPrint_TextFormat(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	code := runPrint(context.Background(), printOptions{
		name:         "Echo",
		sessionID:    "sid-1",
		cwd:          "/tmp",
		outputFormat: "text",
		positional:   []string{"hello", "world"},
		hooks:        newNoopHooks(),
		mcp:          mcp.NewClient(nil),
	}, strings.NewReader(""), stdout)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := strings.TrimRight(stdout.String(), "\n")
	if got != "[Echo] hello world" {
		t.Errorf("text output = %q, want %q", got, "[Echo] hello world")
	}
}

func TestRunPrint_JSONFormat(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	code := runPrint(context.Background(), printOptions{
		name:         "Echo",
		sessionID:    "sid-2",
		cwd:          "/tmp",
		outputFormat: "json",
		positional:   []string{"summarize"},
		hooks:        newNoopHooks(),
		mcp:          mcp.NewClient(nil),
	}, strings.NewReader(""), stdout)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output not valid JSON: %v\nbody: %s", err, stdout.String())
	}
	checkField(t, got, "type", "result")
	checkField(t, got, "subtype", "success")
	checkField(t, got, "is_error", false)
	checkField(t, got, "session_id", "sid-2")
	checkField(t, got, "result", "[Echo] summarize")
	if _, ok := got["duration_api_ms"]; !ok {
		t.Errorf("missing duration_api_ms")
	}
	// Cursor json shape MUST NOT carry claude-specific fields.
	if _, ok := got["usage"]; ok {
		t.Errorf("usage field leaked into cursor json output (claude-only)")
	}
	if _, ok := got["total_cost_usd"]; ok {
		t.Errorf("total_cost_usd leaked into cursor json output (claude-only)")
	}
	if _, ok := got["num_turns"]; ok {
		t.Errorf("num_turns leaked into cursor json output (claude-only)")
	}
}

func TestRunPrint_StreamJSONFormat(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	code := runPrint(context.Background(), printOptions{
		name:         "Echo",
		sessionID:    "sid-3",
		cwd:          "/work",
		model:        "claude-sonnet-4-stub",
		outputFormat: "stream-json",
		positional:   []string{"hi"},
		hooks:        newNoopHooks(),
		mcp:          mcp.NewClient(nil),
	}, strings.NewReader(""), stdout)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (system/user/assistant/result)\nbody:\n%s", len(lines), stdout.String())
	}

	frames := make([]map[string]any, len(lines))
	for i, line := range lines {
		if err := json.Unmarshal([]byte(line), &frames[i]); err != nil {
			t.Fatalf("frame %d not valid JSON: %v\nline: %s", i, err, line)
		}
	}

	// Frame 0: system / init
	checkField(t, frames[0], "type", "system")
	checkField(t, frames[0], "subtype", "init")
	checkField(t, frames[0], "session_id", "sid-3")
	checkField(t, frames[0], "cwd", "/work")
	checkField(t, frames[0], "model", "claude-sonnet-4-stub")
	checkField(t, frames[0], "permissionMode", "default")
	checkField(t, frames[0], "apiKeySource", "none")
	// Cursor system/init MUST NOT include claude-specific fields.
	if _, ok := frames[0]["tools"]; ok {
		t.Errorf("system/init has tools (claude-only)")
	}
	if _, ok := frames[0]["mcp_servers"]; ok {
		t.Errorf("system/init has mcp_servers (claude-only)")
	}

	// Frame 1: user
	checkField(t, frames[1], "type", "user")
	checkField(t, frames[1], "session_id", "sid-3")
	msg1, ok := frames[1]["message"].(map[string]any)
	if !ok {
		t.Fatalf("user.message is not an object")
	}
	checkField(t, msg1, "role", "user")
	content1, ok := msg1["content"].([]any)
	if !ok || len(content1) != 1 {
		t.Fatalf("user.message.content not a single-element array: %v", msg1["content"])
	}
	c1 := content1[0].(map[string]any)
	checkField(t, c1, "type", "text")
	checkField(t, c1, "text", "hi")

	// Frame 2: assistant
	checkField(t, frames[2], "type", "assistant")
	checkField(t, frames[2], "session_id", "sid-3")
	msg2 := frames[2]["message"].(map[string]any)
	checkField(t, msg2, "role", "assistant")
	content2 := msg2["content"].([]any)
	c2 := content2[0].(map[string]any)
	checkField(t, c2, "text", "[Echo] hi")
	// Cursor assistant message MUST NOT carry claude-specific usage/stop_reason.
	if _, ok := msg2["usage"]; ok {
		t.Errorf("assistant.message has usage (claude-only)")
	}
	if _, ok := msg2["stop_reason"]; ok {
		t.Errorf("assistant.message has stop_reason (claude-only)")
	}

	// Frame 3: result
	checkField(t, frames[3], "type", "result")
	checkField(t, frames[3], "subtype", "success")
	checkField(t, frames[3], "is_error", false)
	checkField(t, frames[3], "session_id", "sid-3")
	checkField(t, frames[3], "result", "[Echo] hi")
	if _, ok := frames[3]["permission_denials"]; ok {
		t.Errorf("result has permission_denials (claude-only)")
	}
	if _, ok := frames[3]["uuid"]; ok {
		t.Errorf("result has uuid (claude-only)")
	}
}

func TestRunPrint_StdinFallback(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	code := runPrint(context.Background(), printOptions{
		name:         "Echo",
		sessionID:    "sid-4",
		outputFormat: "text",
		hooks:        newNoopHooks(),
		mcp:          mcp.NewClient(nil),
	}, strings.NewReader("from stdin\n"), stdout)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := strings.TrimRight(stdout.String(), "\n")
	if got != "[Echo] from stdin" {
		t.Errorf("stdin fallback output = %q, want %q", got, "[Echo] from stdin")
	}
}

func TestRunPrint_MissingPrompt(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	code := runPrint(context.Background(), printOptions{
		name:         "Echo",
		sessionID:    "sid-5",
		outputFormat: "text",
		hooks:        newNoopHooks(),
		mcp:          mcp.NewClient(nil),
	}, strings.NewReader(""), stdout)

	if code != 1 {
		t.Errorf("missing-prompt exit code = %d, want 1", code)
	}
}

// checkField mirrors the helper in cmd/claude/print_test.go. Duplicated
// rather than imported to keep each vendor package's tests independent.
func checkField(t *testing.T, m map[string]any, key string, want any) {
	t.Helper()
	got, ok := m[key]
	if !ok {
		t.Errorf("missing field %q", key)
		return
	}
	if got != want {
		t.Errorf("field %q = %v, want %v", key, got, want)
	}
}
