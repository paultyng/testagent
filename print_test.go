package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
)

func TestRunPrint_TextFormat(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	code := runPrint(context.Background(), printOptions{
		name:         "Echo",
		sessionID:    "sid-1",
		cwd:          "/tmp",
		outputFormat: "text",
		positional:   []string{"hello", "world"},
		hooks:        hooks.NewSender(nil, "sid-1", "/tmp", "", "default", nil),
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
		hooks:        hooks.NewSender(nil, "sid-2", "/tmp", "", "default", nil),
		mcp:          mcp.NewClient(nil),
	}, strings.NewReader(""), stdout)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, stdout.String())
	}
	checkField(t, got, "type", "result")
	checkField(t, got, "subtype", "success")
	checkField(t, got, "is_error", false)
	checkField(t, got, "session_id", "sid-2")
	checkField(t, got, "result", "[Echo] summarize")
	if _, ok := got["usage"]; !ok {
		t.Errorf("missing usage field")
	}
}

func TestRunPrint_StreamJSONFormat(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	code := runPrint(context.Background(), printOptions{
		name:         "Echo",
		sessionID:    "sid-3",
		cwd:          "/work",
		outputFormat: "stream-json",
		positional:   []string{"do", "the", "thing"},
		hooks:        hooks.NewSender(nil, "sid-3", "/work", "", "default", nil),
		mcp:          mcp.NewClient(nil),
	}, strings.NewReader(""), stdout)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (system+assistant+result)", len(lines))
	}

	var sys, asst, res map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &sys); err != nil {
		t.Fatalf("line 1 not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &asst); err != nil {
		t.Fatalf("line 2 not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[2]), &res); err != nil {
		t.Fatalf("line 3 not JSON: %v", err)
	}

	checkField(t, sys, "type", "system")
	checkField(t, sys, "subtype", "init")
	checkField(t, sys, "session_id", "sid-3")
	checkField(t, sys, "cwd", "/work")

	checkField(t, asst, "type", "assistant")
	if msg, ok := asst["message"].(map[string]any); !ok {
		t.Errorf("assistant.message not a map")
	} else {
		checkField(t, msg, "role", "assistant")
		checkField(t, msg, "stop_reason", "end_turn")
		if content, ok := msg["content"].([]any); !ok || len(content) != 1 {
			t.Errorf("assistant.message.content = %v, want one entry", msg["content"])
		}
	}

	checkField(t, res, "type", "result")
	checkField(t, res, "subtype", "success")
	checkField(t, res, "result", "[Echo] do the thing")
}

func TestRunPrint_StdinFallback(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	code := runPrint(context.Background(), printOptions{
		name:         "Echo",
		sessionID:    "sid-4",
		outputFormat: "text",
		hooks:        hooks.NewSender(nil, "sid-4", "/tmp", "", "default", nil),
		mcp:          mcp.NewClient(nil),
	}, strings.NewReader("piped prompt\n"), stdout)

	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	got := strings.TrimRight(stdout.String(), "\n")
	if got != "[Echo] piped prompt" {
		t.Errorf("got %q, want %q", got, "[Echo] piped prompt")
	}
}

func TestRunPrint_MissingPrompt(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	code := runPrint(context.Background(), printOptions{
		name:         "Echo",
		sessionID:    "sid-5",
		outputFormat: "text",
		hooks:        hooks.NewSender(nil, "sid-5", "/tmp", "", "default", nil),
		mcp:          mcp.NewClient(nil),
	}, strings.NewReader(""), stdout)

	if code == 0 {
		t.Errorf("exit code = 0 with no prompt; want non-zero")
	}
}

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
