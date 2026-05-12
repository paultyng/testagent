package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/paultyng/testagent/internal/codexhooks"
)

// newTestRunner returns a no-hook codex Runner suitable for exec tests.
func newTestRunner(sid string) *codexhooks.Runner {
	return codexhooks.NewRunner(nil, sid, "/tmp", "", "default", nil)
}

func TestRunExec_TextFormat(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	err := runExec(context.Background(), execOptions{
		name:         "session",
		sessionID:    "sid-1",
		cwd:          "/tmp",
		outputFormat: "text",
		positional:   []string{"hello", "world"},
		hooks:        newTestRunner("sid-1"),
	}, strings.NewReader(""), stdout)

	if err != nil {
		t.Fatalf("runExec err = %v", err)
	}
	got := strings.TrimRight(stdout.String(), "\n")
	if got != "[session] hello world" {
		t.Errorf("text output = %q, want %q", got, "[session] hello world")
	}
}

func TestRunExec_JSONFormat(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	err := runExec(context.Background(), execOptions{
		name:         "session",
		sessionID:    "sid-2",
		outputFormat: "json",
		positional:   []string{"summarize"},
		hooks:        newTestRunner("sid-2"),
	}, strings.NewReader(""), stdout)
	if err != nil {
		t.Fatalf("runExec err = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, stdout.String())
	}
	if got["type"] != "turn.completed" {
		t.Errorf("type = %v, want turn.completed", got["type"])
	}
	if got["thread_id"] != "sid-2" {
		t.Errorf("thread_id = %v, want sid-2", got["thread_id"])
	}
	if got["final_message"] != "[session] summarize" {
		t.Errorf("final_message = %v, want [session] summarize", got["final_message"])
	}
	usage, ok := got["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage not an object: %v", got["usage"])
	}
	for _, k := range []string{"input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens"} {
		if _, ok := usage[k]; !ok {
			t.Errorf("usage missing key %q", k)
		}
	}
}

func TestRunExec_StreamJSONFormat(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	err := runExec(context.Background(), execOptions{
		name:         "session",
		sessionID:    "sid-3",
		cwd:          "/work",
		outputFormat: "stream-json",
		positional:   []string{"do", "the", "thing"},
		hooks:        newTestRunner("sid-3"),
	}, strings.NewReader(""), stdout)
	if err != nil {
		t.Fatalf("runExec err = %v", err)
	}

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5 (thread.started + turn.started + item.started + item.completed + turn.completed)\noutput:\n%s", len(lines), stdout.String())
	}
	frames := make([]map[string]any, len(lines))
	for i, ln := range lines {
		if err := json.Unmarshal([]byte(ln), &frames[i]); err != nil {
			t.Fatalf("line %d not JSON: %v\n%s", i+1, err, ln)
		}
	}

	if frames[0]["type"] != "thread.started" || frames[0]["thread_id"] != "sid-3" {
		t.Errorf("frame 0 = %v, want thread.started/sid-3", frames[0])
	}
	if frames[1]["type"] != "turn.started" {
		t.Errorf("frame 1 type = %v, want turn.started", frames[1]["type"])
	}
	if frames[2]["type"] != "item.started" {
		t.Errorf("frame 2 type = %v, want item.started", frames[2]["type"])
	}
	if frames[3]["type"] != "item.completed" {
		t.Errorf("frame 3 type = %v, want item.completed", frames[3]["type"])
	}
	for _, idx := range []int{2, 3} {
		item, ok := frames[idx]["item"].(map[string]any)
		if !ok {
			t.Fatalf("frame %d item not an object: %v", idx, frames[idx]["item"])
		}
		if item["type"] != "agent_message" {
			t.Errorf("frame %d item.type = %v, want agent_message", idx, item["type"])
		}
		if item["text"] != "[session] do the thing" {
			t.Errorf("frame %d item.text = %v, want [session] do the thing", idx, item["text"])
		}
	}
	if frames[4]["type"] != "turn.completed" {
		t.Errorf("frame 4 type = %v, want turn.completed", frames[4]["type"])
	}
	if _, ok := frames[4]["usage"].(map[string]any); !ok {
		t.Errorf("frame 4 usage not an object: %v", frames[4]["usage"])
	}
}

func TestRunExec_StdinFallback(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	err := runExec(context.Background(), execOptions{
		name:         "session",
		sessionID:    "sid-4",
		outputFormat: "text",
		hooks:        newTestRunner("sid-4"),
	}, strings.NewReader("piped prompt\n"), stdout)
	if err != nil {
		t.Fatalf("runExec err = %v", err)
	}
	got := strings.TrimRight(stdout.String(), "\n")
	if got != "[session] piped prompt" {
		t.Errorf("got %q, want %q", got, "[session] piped prompt")
	}
}

func TestRunExec_MissingPrompt(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	err := runExec(context.Background(), execOptions{
		name:         "session",
		sessionID:    "sid-5",
		outputFormat: "text",
		hooks:        newTestRunner("sid-5"),
	}, strings.NewReader(""), stdout)
	if err == nil {
		t.Errorf("err = nil, want non-nil for missing prompt")
	}
}

// TestRunExec_FiresLifecycleHooks asserts session_start →
// user_prompt_submit → stop fire in order against the codex runner.
// Uses shell-command hooks (codex's hook shape) that append to a log
// file so the test can verify ordering.
func TestRunExec_FiresLifecycleHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-command hook ordering uses POSIX append semantics; skipped on Windows")
	}
	t.Parallel()

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "events.log")

	matchers := map[string][]codexhooks.Matcher{
		codexhooks.EventSessionStart:     {{Command: "echo session_start >> " + logPath, Timeout: 5}},
		codexhooks.EventUserPromptSubmit: {{Command: "echo user_prompt_submit >> " + logPath, Timeout: 5}},
		codexhooks.EventStop:             {{Command: "echo stop >> " + logPath, Timeout: 5}},
	}
	runner := codexhooks.NewRunner(matchers, "sid-life", tmp, "", "default", nil)

	stdout := &bytes.Buffer{}
	err := runExec(context.Background(), execOptions{
		name:         "session",
		sessionID:    "sid-life",
		cwd:          tmp,
		outputFormat: "text",
		positional:   []string{"hi"},
		hooks:        runner,
	}, strings.NewReader(""), stdout)
	if err != nil {
		t.Fatalf("runExec err = %v", err)
	}

	raw, rerr := os.ReadFile(logPath)
	if rerr != nil {
		t.Fatalf("read events log: %v", rerr)
	}
	var events []string
	for _, ln := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if ln != "" {
			events = append(events, ln)
		}
	}

	want := []string{"session_start", "user_prompt_submit", "stop"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("events[%d] = %q, want %q (full: %v)", i, events[i], want[i], events)
		}
	}
}
