package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestSlashHandler(out *bytes.Buffer) *SlashHandler {
	return &SlashHandler{
		name:        "Test",
		streamDelay: 0,
		sessionID:   "sid-test",
		cwd:         "/tmp",
		hooks:       NewHookSender(nil, "sid-test", "/tmp", "", "default", nil),
		mcp:         NewMCPClient(nil),
		out:         out,
	}
}

func TestSlash_NotASlash(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	got := h.Dispatch(context.Background(), "regular prompt")
	if got.Handled {
		t.Errorf("non-slash input should not be handled")
	}
	if out.Len() != 0 {
		t.Errorf("non-slash input should not write output, got %q", out.String())
	}
}

func TestSlash_Stream(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.Dispatch(context.Background(), "/stream hello world")
	got := strings.TrimRight(out.String(), "\n")
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestSlash_Think(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.Dispatch(context.Background(), "/think pondering deeply")
	if !strings.Contains(out.String(), "pondering deeply") {
		t.Errorf("output missing think text: %q", out.String())
	}
}

func TestSlash_Panel(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.Dispatch(context.Background(), "/panel a panel message")
	s := out.String()
	// lipgloss uses rounded corners (╭ ╰); in no-color test env still draws box chars.
	hasBorder := strings.Contains(s, "╭") || strings.Contains(s, "─")
	if !hasBorder {
		t.Errorf("panel missing border chars: %q", s)
	}
	if !strings.Contains(s, "a panel message") {
		t.Errorf("panel missing content")
	}
}

func TestSlash_ToolAlone_NoHookYet(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.hooks = NewHookSender(&Settings{
		Hooks: map[string][]HookMatcher{
			"PostToolUse": {{Hooks: []Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
		},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/tool read_file {"path":"foo.go"}`)

	if !strings.Contains(out.String(), "read_file") {
		t.Errorf("output missing tool name: %q", out.String())
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("/tool alone fired %d hook(s); want 0 (paired with /result)", got)
	}
}

func TestSlash_ToolResultPair(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		captured []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		captured = append(captured, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.hooks = NewHookSender(&Settings{
		Hooks: map[string][]HookMatcher{
			"PostToolUse": {{Hooks: []Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
		},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/tool read_file {"path":"foo.go"}`)
	time.Sleep(2 * time.Millisecond) // ensure non-zero duration_ms
	h.Dispatch(context.Background(), `/result {"contents":"package foo"}`)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("got %d hook calls, want 1", len(captured))
	}
	body := captured[0]
	if body["tool_name"] != "read_file" {
		t.Errorf("tool_name = %v, want read_file", body["tool_name"])
	}
	input, _ := body["tool_input"].(map[string]any)
	if input == nil || input["path"] != "foo.go" {
		t.Errorf("tool_input = %v, want {path:foo.go}", body["tool_input"])
	}
	resp, _ := body["tool_response"].(map[string]any)
	if resp == nil || resp["contents"] != "package foo" {
		t.Errorf("tool_response = %v, want {contents:package foo}", body["tool_response"])
	}
	dur, _ := body["duration_ms"].(float64)
	if dur < 1 {
		t.Errorf("duration_ms = %v, want >= 1ms", body["duration_ms"])
	}
}

func TestSlash_OrphanResult(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.hooks = NewHookSender(&Settings{
		Hooks: map[string][]HookMatcher{
			"PostToolUse": {{Hooks: []Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
		},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/result {"orphan":true}`)

	if !strings.Contains(out.String(), "orphan") {
		t.Errorf("output missing result body: %q", out.String())
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("orphan /result fired %d hook(s); want 0", got)
	}
}

func TestSlash_FlushPendingTool(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		captured []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		captured = append(captured, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.hooks = NewHookSender(&Settings{
		Hooks: map[string][]HookMatcher{
			"PostToolUse": {{Hooks: []Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
		},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/tool dangling {}`)
	h.FlushPendingTool(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("got %d hook calls, want 1", len(captured))
	}
	if captured[0]["tool_name"] != "dangling" {
		t.Errorf("tool_name = %v, want dangling", captured[0]["tool_name"])
	}
	if captured[0]["tool_response"] != nil {
		t.Errorf("tool_response = %v, want nil", captured[0]["tool_response"])
	}
}

func TestSlash_SecondToolReplacesPending(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		captured []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		captured = append(captured, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.hooks = NewHookSender(&Settings{
		Hooks: map[string][]HookMatcher{
			"PostToolUse": {{Hooks: []Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
		},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/tool first {}`)
	h.Dispatch(context.Background(), `/tool second {}`)
	h.Dispatch(context.Background(), `/result {"ok":true}`)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("got %d hook calls, want 2", len(captured))
	}
	if captured[0]["tool_name"] != "first" {
		t.Errorf("first tool_name = %v, want first", captured[0]["tool_name"])
	}
	if captured[0]["tool_response"] != nil {
		t.Errorf("first tool_response = %v, want nil (flushed)", captured[0]["tool_response"])
	}
	if captured[1]["tool_name"] != "second" {
		t.Errorf("second tool_name = %v, want second", captured[1]["tool_name"])
	}
	if resp, _ := captured[1]["tool_response"].(map[string]any); resp == nil || resp["ok"] != true {
		t.Errorf("second tool_response = %v, want {ok:true}", captured[1]["tool_response"])
	}
}

func TestSlash_Result(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	h.Dispatch(context.Background(), `/result {"ok":true}`)
	s := out.String()
	if !strings.Contains(s, "ok") {
		t.Errorf("result missing field: %q", s)
	}
	if !strings.Contains(s, "✓") {
		t.Errorf("result missing checkmark: %q", s)
	}
}

func TestSlash_Exit(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)

	noCode := h.Dispatch(context.Background(), "/exit")
	if !noCode.Exit || noCode.ExitCode != 0 {
		t.Errorf("/exit got Exit=%v Code=%d, want true/0", noCode.Exit, noCode.ExitCode)
	}

	withCode := h.Dispatch(context.Background(), "/exit 7")
	if !withCode.Exit || withCode.ExitCode != 7 {
		t.Errorf("/exit 7 got Exit=%v Code=%d, want true/7", withCode.Exit, withCode.ExitCode)
	}
}

func TestSlash_UnknownCommand(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestSlashHandler(out)
	got := h.Dispatch(context.Background(), "/notacommand foo")
	if !got.Handled {
		t.Errorf("unknown slash should still be Handled (consumed); got %+v", got)
	}
}

func TestSlash_DispatchString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		line      string
		wantBody  string
		wantExit  bool
		wantHand  bool
		wantInOut string // substring assertion when wantBody is empty
	}{
		{
			name:      "stream returns rendered text",
			line:      "/stream hello world",
			wantInOut: "hello world",
			wantHand:  true,
		},
		{
			name:     "exit returns outcome but empty body",
			line:     "/exit 3",
			wantBody: "",
			wantExit: true,
			wantHand: true,
		},
		{
			name:      "panel rendered body matches Dispatch",
			line:      "/panel boxed",
			wantInOut: "boxed",
			wantHand:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Run via DispatchString (string-returning).
			h1 := newTestSlashHandler(&bytes.Buffer{})
			body, outcome := h1.DispatchString(context.Background(), tc.line)

			if outcome.Handled != tc.wantHand {
				t.Errorf("DispatchString outcome.Handled = %v, want %v", outcome.Handled, tc.wantHand)
			}
			if outcome.Exit != tc.wantExit {
				t.Errorf("DispatchString outcome.Exit = %v, want %v", outcome.Exit, tc.wantExit)
			}
			if tc.wantInOut != "" && !strings.Contains(body, tc.wantInOut) {
				t.Errorf("DispatchString body missing %q:\n%s", tc.wantInOut, body)
			}
			if tc.wantBody != "" && body != tc.wantBody {
				t.Errorf("DispatchString body = %q, want %q", body, tc.wantBody)
			}

			// Run via buffered Dispatch (the legacy path) and compare bodies.
			out := &bytes.Buffer{}
			h2 := newTestSlashHandler(out)
			outcome2 := h2.Dispatch(context.Background(), tc.line)
			if outcome2.Exit != outcome.Exit || outcome2.ExitCode != outcome.ExitCode || outcome2.Handled != outcome.Handled {
				t.Errorf("Dispatch outcome mismatch:\n  DispatchString=%+v\n  Dispatch=%+v", outcome, outcome2)
			}
			if out.String() != body {
				t.Errorf("rendered string differs between Dispatch and DispatchString:\n  Dispatch=%q\n  DispatchString=%q", out.String(), body)
			}
		})
	}
}

func TestSplitFirstWord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, head, tail string
	}{
		{"foo bar baz", "foo", "bar baz"},
		{"  foo bar", "foo", "bar"},
		{"singleword", "singleword", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		head, tail := splitFirstWord(tc.in)
		if head != tc.head || tail != tc.tail {
			t.Errorf("splitFirstWord(%q) = (%q, %q), want (%q, %q)", tc.in, head, tail, tc.head, tc.tail)
		}
	}
}

func readAll(r interface {
	Read([]byte) (int, error)
}) ([]byte, error) {
	var buf bytes.Buffer
	tmp := make([]byte, 512)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf.Bytes(), nil
			}
			return buf.Bytes(), err
		}
	}
}
