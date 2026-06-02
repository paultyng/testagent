package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/slash"
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

// TestRunPrint_LeadingSlashDispatch covers the -p / --print pre-prompt
// dispatch path: lines starting with "/" are routed through the slash
// handler in order; the first non-slash line (and everything after it)
// becomes the prompt that's echoed.
func TestRunPrint_LeadingSlashDispatch(t *testing.T) {
	t.Parallel()

	t.Run("pure side-effect skips echo", func(t *testing.T) {
		t.Parallel()
		stdout := &bytes.Buffer{}
		sender := hooks.NewSender(nil, "sid-s1", "/tmp", "", "default", nil)
		mcpClient := mcp.NewClient(nil)
		code := runPrint(context.Background(), printOptions{
			name:         "Echo",
			sessionID:    "sid-s1",
			cwd:          "/tmp",
			outputFormat: "text",
			positional:   []string{"/panel hello"},
			hooks:        sender,
			mcp:          mcpClient,
			slash:        slash.New(sender, mcpClient, stdout),
		}, strings.NewReader(""), stdout)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		out := stdout.String()
		if !strings.Contains(out, "hello") {
			t.Errorf("/panel render missing 'hello' in output: %q", out)
		}
		if strings.Contains(out, "[Echo]") {
			t.Errorf("echo path should be skipped, got [Echo] prefix: %q", out)
		}
	})

	t.Run("exit slash propagates code", func(t *testing.T) {
		t.Parallel()
		stdout := &bytes.Buffer{}
		sender := hooks.NewSender(nil, "sid-s2", "/tmp", "", "default", nil)
		mcpClient := mcp.NewClient(nil)
		code := runPrint(context.Background(), printOptions{
			name:         "Echo",
			sessionID:    "sid-s2",
			cwd:          "/tmp",
			outputFormat: "text",
			positional:   []string{"/exit 7"},
			hooks:        sender,
			mcp:          mcpClient,
			slash:        slash.New(sender, mcpClient, stdout),
		}, strings.NewReader(""), stdout)
		if code != 7 {
			t.Fatalf("exit code = %d, want 7", code)
		}
	})

	t.Run("think sleeps then echoes parsed message", func(t *testing.T) {
		t.Parallel()
		stdout := &bytes.Buffer{}
		sender := hooks.NewSender(nil, "sid-s3", "/tmp", "", "default", nil)
		mcpClient := mcp.NewClient(nil)
		start := time.Now()
		code := runPrint(context.Background(), printOptions{
			name:         "Echo",
			sessionID:    "sid-s3",
			cwd:          "/tmp",
			outputFormat: "text",
			positional:   []string{"/think 40ms hello there"},
			hooks:        sender,
			mcp:          mcpClient,
			slash:        slash.New(sender, mcpClient, stdout),
		}, strings.NewReader(""), stdout)
		elapsed := time.Since(start)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if elapsed < 40*time.Millisecond {
			t.Errorf("elapsed = %v, want >= 40ms (/think delay was not honored)", elapsed)
		}
		got := strings.TrimRight(stdout.String(), "\n")
		if got != "[Echo] hello there" {
			t.Errorf("echo = %q, want %q", got, "[Echo] hello there")
		}
	})

	t.Run("multi-line: side-effects then prompt", func(t *testing.T) {
		t.Parallel()
		stdout := &bytes.Buffer{}
		sender := hooks.NewSender(nil, "sid-s4", "/tmp", "", "default", nil)
		mcpClient := mcp.NewClient(nil)
		input := "/panel banner\n/panel second\nthe real prompt"
		code := runPrint(context.Background(), printOptions{
			name:         "Echo",
			sessionID:    "sid-s4",
			cwd:          "/tmp",
			outputFormat: "text",
			positional:   []string{input},
			hooks:        sender,
			mcp:          mcpClient,
			slash:        slash.New(sender, mcpClient, stdout),
		}, strings.NewReader(""), stdout)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		out := stdout.String()
		if !strings.Contains(out, "banner") || !strings.Contains(out, "second") {
			t.Errorf("missing panel renders in output: %q", out)
		}
		if !strings.Contains(out, "[Echo] the real prompt") {
			t.Errorf("missing echoed prompt in output: %q", out)
		}
	})

	t.Run("multi-line: first non-slash includes trailing lines", func(t *testing.T) {
		t.Parallel()
		stdout := &bytes.Buffer{}
		sender := hooks.NewSender(nil, "sid-s5", "/tmp", "", "default", nil)
		mcpClient := mcp.NewClient(nil)
		input := "/panel hi\nfirst line\nsecond line"
		code := runPrint(context.Background(), printOptions{
			name:         "Echo",
			sessionID:    "sid-s5",
			cwd:          "/tmp",
			outputFormat: "text",
			positional:   []string{input},
			hooks:        sender,
			mcp:          mcpClient,
			slash:        slash.New(sender, mcpClient, stdout),
		}, strings.NewReader(""), stdout)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if !strings.Contains(stdout.String(), "[Echo] first line\nsecond line") {
			t.Errorf("multi-line prompt not joined into echo: %q", stdout.String())
		}
	})

	t.Run("nil slash handler preserves legacy echo for /-prefixed prompt", func(t *testing.T) {
		t.Parallel()
		stdout := &bytes.Buffer{}
		sender := hooks.NewSender(nil, "sid-s6", "/tmp", "", "default", nil)
		code := runPrint(context.Background(), printOptions{
			name:         "Echo",
			sessionID:    "sid-s6",
			cwd:          "/tmp",
			outputFormat: "text",
			positional:   []string{"/help"},
			hooks:        sender,
			mcp:          mcp.NewClient(nil),
			// slash intentionally nil — exercises the back-compat path.
		}, strings.NewReader(""), stdout)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if strings.TrimRight(stdout.String(), "\n") != "[Echo] /help" {
			t.Errorf("nil-slash path should echo as-is; got %q", stdout.String())
		}
	})
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

// TestRunPrint_SessionStartHonorsResume verifies the SessionStart hook
// fires with source="resume" when printOptions.resumed is true, matching
// the interactive code path. Closes #25.
func TestRunPrint_SessionStartHonorsResume(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		resumed    bool
		wantSource string
	}{
		{name: "fresh session", resumed: false, wantSource: "startup"},
		{name: "resumed session", resumed: true, wantSource: "resume"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu  sync.Mutex
				got string
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				var body map[string]any
				_ = json.Unmarshal(raw, &body)
				if name, _ := body["hook_event_name"].(string); name == "SessionStart" {
					mu.Lock()
					got, _ = body["source"].(string)
					mu.Unlock()
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			matchers := map[string][]hooks.Matcher{
				hooks.SessionStart: {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
			}
			sender := hooks.NewSender(matchers, "sid-resume", "/tmp", "", "default", nil)

			stdout := &bytes.Buffer{}
			code := runPrint(context.Background(), printOptions{
				name:         "Echo",
				sessionID:    "sid-resume",
				cwd:          "/tmp",
				outputFormat: "text",
				positional:   []string{"hi"},
				resumed:      tc.resumed,
				hooks:        sender,
				mcp:          mcp.NewClient(nil),
			}, strings.NewReader(""), stdout)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}

			mu.Lock()
			defer mu.Unlock()
			if got != tc.wantSource {
				t.Errorf("SessionStart source = %q, want %q", got, tc.wantSource)
			}
		})
	}
}
