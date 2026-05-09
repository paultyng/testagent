package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captured holds a single received POST for assertion.
type captured struct {
	headers http.Header
	body    map[string]any
	path    string
}

// captureServer spins up an httptest.Server that records all POSTs.
func captureServer(t *testing.T) (*httptest.Server, *[]captured, *sync.Mutex) {
	t.Helper()
	var (
		mu   sync.Mutex
		recs []captured
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var body map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("unmarshal body: %v (raw=%s)", err, raw)
			}
		}
		mu.Lock()
		recs = append(recs, captured{
			headers: r.Header.Clone(),
			body:    body,
			path:    r.URL.Path,
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &recs, &mu
}

// settingsWithHooks builds a Settings whose event maps to a single matcher with
// one HTTP hook per supplied URL. Headers are applied to every hook.
func settingsWithHooks(event string, headers map[string]string, urls ...string) *Settings {
	hooks := make([]Hook, 0, len(urls))
	for _, u := range urls {
		hooks = append(hooks, Hook{Type: "http", URL: u, Headers: headers})
	}
	return &Settings{
		Hooks: map[string][]HookMatcher{
			event: {{Matcher: "*", Hooks: hooks}},
		},
	}
}

func newTestSender(t *testing.T, settings *Settings) *HookSender {
	t.Helper()
	return NewHookSender(settings, "session-xyz", "/tmp/cwd", "/tmp/transcript.jsonl", "auto")
}

func TestHookSender_NilSettings_NoOp(t *testing.T) {
	t.Parallel()
	sender := newTestSender(t, nil)
	ctx := context.Background()
	if err := sender.OnPrompt(ctx, "hi", "title"); err != nil {
		t.Errorf("OnPrompt: %v", err)
	}
	if err := sender.OnToolUse(ctx, "tu_1", "Bash", map[string]any{"cmd": "ls"}, "ok", 5); err != nil {
		t.Errorf("OnToolUse: %v", err)
	}
	if err := sender.OnStop(ctx, "bye", false); err != nil {
		t.Errorf("OnStop: %v", err)
	}
	if err := sender.OnSessionEnd(ctx, "clear"); err != nil {
		t.Errorf("OnSessionEnd: %v", err)
	}
}

func TestHookSender_NoMatchingEvent_NoOp(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	// Settings register Stop only — OnPrompt should be a no-op.
	settings := settingsWithHooks(hookEventStop, nil, srv.URL+"/hooks/stop")
	sender := newTestSender(t, settings)
	if err := sender.OnPrompt(context.Background(), "hi", "title"); err != nil {
		t.Fatalf("OnPrompt: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 0 {
		t.Errorf("expected no requests, got %d", len(*recs))
	}
}

func TestHookSender_OnPrompt_PayloadAndHeaders(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	headers := map[string]string{"X-Session-Id": "abc-123"}
	settings := settingsWithHooks(hookEventUserPromptSubmit, headers, srv.URL+"/hooks/prompt")
	sender := newTestSender(t, settings)

	if err := sender.OnPrompt(context.Background(), "hello world", "My Session"); err != nil {
		t.Fatalf("OnPrompt: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	if rec.path != "/hooks/prompt" {
		t.Errorf("path = %q, want /hooks/prompt", rec.path)
	}
	if got := rec.headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := rec.headers.Get("X-Session-Id"); got != "abc-123" {
		t.Errorf("X-Session-Id = %q, want abc-123", got)
	}
	wantFields := map[string]any{
		"cwd":             "/tmp/cwd",
		"hook_event_name": "UserPromptSubmit",
		"permission_mode": "auto",
		"prompt":          "hello world",
		"session_id":      "session-xyz",
		"session_title":   "My Session",
		"transcript_path": "/tmp/transcript.jsonl",
	}
	for k, want := range wantFields {
		if got := rec.body[k]; got != want {
			t.Errorf("body[%s] = %v, want %v", k, got, want)
		}
	}
}

func TestHookSender_OnToolUse_PayloadAndHeaders(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	settings := settingsWithHooks(hookEventPostToolUse, map[string]string{"X-Idea-Slug": "demo"}, srv.URL+"/hooks/tool-use")
	sender := newTestSender(t, settings)

	toolInput := map[string]any{"command": "ls -la"}
	toolResponse := map[string]any{"stdout": "file1\nfile2", "exit_code": float64(0)}
	if err := sender.OnToolUse(context.Background(), "tu_42", "Bash", toolInput, toolResponse, 1234); err != nil {
		t.Fatalf("OnToolUse: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	if got := rec.headers.Get("X-Idea-Slug"); got != "demo" {
		t.Errorf("X-Idea-Slug = %q, want demo", got)
	}
	if got := rec.body["hook_event_name"]; got != "PostToolUse" {
		t.Errorf("hook_event_name = %v", got)
	}
	if got := rec.body["tool_name"]; got != "Bash" {
		t.Errorf("tool_name = %v", got)
	}
	if got := rec.body["tool_use_id"]; got != "tu_42" {
		t.Errorf("tool_use_id = %v", got)
	}
	// JSON numbers decode as float64.
	if got := rec.body["duration_ms"]; got != float64(1234) {
		t.Errorf("duration_ms = %v, want 1234", got)
	}
	gotInput, ok := rec.body["tool_input"].(map[string]any)
	if !ok {
		t.Fatalf("tool_input is not an object: %T", rec.body["tool_input"])
	}
	if gotInput["command"] != "ls -la" {
		t.Errorf("tool_input.command = %v", gotInput["command"])
	}
	gotResponse, ok := rec.body["tool_response"].(map[string]any)
	if !ok {
		t.Fatalf("tool_response is not an object: %T", rec.body["tool_response"])
	}
	if gotResponse["stdout"] != "file1\nfile2" {
		t.Errorf("tool_response.stdout = %v", gotResponse["stdout"])
	}
	for _, k := range []string{"cwd", "permission_mode", "session_id", "transcript_path"} {
		if rec.body[k] == nil {
			t.Errorf("body[%s] missing", k)
		}
	}
}

func TestHookSender_OnStop_Payload(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	settings := settingsWithHooks(hookEventStop, nil, srv.URL+"/hooks/stop")
	sender := newTestSender(t, settings)

	if err := sender.OnStop(context.Background(), "all done", true); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	wantFields := map[string]any{
		"cwd":                    "/tmp/cwd",
		"hook_event_name":        "Stop",
		"last_assistant_message": "all done",
		"permission_mode":        "auto",
		"session_id":             "session-xyz",
		"stop_hook_active":       true,
		"transcript_path":        "/tmp/transcript.jsonl",
	}
	for k, want := range wantFields {
		if got := rec.body[k]; got != want {
			t.Errorf("body[%s] = %v, want %v", k, got, want)
		}
	}
}

func TestHookSender_OnSessionEnd_Payload(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	settings := settingsWithHooks(hookEventSessionEnd, nil, srv.URL+"/hooks/end")
	sender := newTestSender(t, settings)

	if err := sender.OnSessionEnd(context.Background(), "clear"); err != nil {
		t.Fatalf("OnSessionEnd: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	wantFields := map[string]any{
		"cwd":             "/tmp/cwd",
		"hook_event_name": "SessionEnd",
		"reason":          "clear",
		"session_id":      "session-xyz",
		"transcript_path": "/tmp/transcript.jsonl",
	}
	for k, want := range wantFields {
		if got := rec.body[k]; got != want {
			t.Errorf("body[%s] = %v, want %v", k, got, want)
		}
	}
}

func TestHookSender_MultipleHooksFire(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	// Two HTTP hooks under one matcher, plus a second matcher with a third hook.
	settings := &Settings{
		Hooks: map[string][]HookMatcher{
			hookEventUserPromptSubmit: {
				{
					Matcher: "*",
					Hooks: []Hook{
						{Type: "http", URL: srv.URL + "/a"},
						{Type: "http", URL: srv.URL + "/b"},
					},
				},
				{
					Matcher: "*",
					Hooks: []Hook{
						{Type: "http", URL: srv.URL + "/c"},
					},
				},
			},
		},
	}
	sender := newTestSender(t, settings)
	if err := sender.OnPrompt(context.Background(), "p", "t"); err != nil {
		t.Fatalf("OnPrompt: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(*recs))
	}
	seen := map[string]bool{}
	for _, r := range *recs {
		seen[r.path] = true
	}
	for _, p := range []string{"/a", "/b", "/c"} {
		if !seen[p] {
			t.Errorf("missing request to %s", p)
		}
	}
}

func TestHookSender_NonHTTPHookSkipped(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	settings := &Settings{
		Hooks: map[string][]HookMatcher{
			hookEventStop: {
				{
					Matcher: "*",
					Hooks: []Hook{
						{Type: "command", URL: "shouldnotfire"},
						{Type: "http", URL: srv.URL + "/hooks/stop"},
					},
				},
			},
		},
	}
	sender := newTestSender(t, settings)
	if err := sender.OnStop(context.Background(), "msg", false); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Errorf("expected 1 http request, got %d", len(*recs))
	}
}

func TestHookSender_OneHookFailingDoesNotBlockOthers(t *testing.T) {
	t.Parallel()
	var goodHits int32
	goodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&goodHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(goodSrv.Close)
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(badSrv.Close)

	settings := &Settings{
		Hooks: map[string][]HookMatcher{
			hookEventStop: {
				{
					Matcher: "*",
					Hooks: []Hook{
						{Type: "http", URL: badSrv.URL + "/bad"},
						{Type: "http", URL: goodSrv.URL + "/good"},
					},
				},
			},
		},
	}
	sender := newTestSender(t, settings)
	err := sender.OnStop(context.Background(), "msg", false)
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	if got := atomic.LoadInt32(&goodHits); got != 1 {
		t.Errorf("good server hits = %d, want 1 (failing hook should not block others)", got)
	}
}

func TestHookSender_HookTimeoutHonored(t *testing.T) {
	t.Parallel()
	// Server that blocks until either the request context is canceled or the
	// test ends. Cleanup order (LIFO) must close `block` BEFORE srv.Close so
	// any hung handler exits and srv.Close does not deadlock.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-block:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	settings := &Settings{
		Hooks: map[string][]HookMatcher{
			hookEventStop: {{
				Matcher: "*",
				Hooks:   []Hook{{Type: "http", URL: srv.URL, Timeout: 1}}, // 1 second
			}},
		},
	}
	sender := newTestSender(t, settings)
	start := time.Now()
	err := sender.OnStop(context.Background(), "msg", false)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("hook took %s — timeout not honored", elapsed)
	}
}

func TestHookSender_DefaultTimeoutWhenZero(t *testing.T) {
	t.Parallel()
	// Hook.Timeout=0 should fall back to defaultHookTimeout, not be zero.
	// We assert behavior by checking the request reaches the server quickly.
	srv, recs, mu := captureServer(t)
	settings := &Settings{
		Hooks: map[string][]HookMatcher{
			hookEventSessionEnd: {{
				Matcher: "*",
				Hooks:   []Hook{{Type: "http", URL: srv.URL, Timeout: 0}},
			}},
		},
	}
	sender := newTestSender(t, settings)
	if err := sender.OnSessionEnd(context.Background(), "clear"); err != nil {
		t.Fatalf("OnSessionEnd: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Errorf("expected 1 request, got %d", len(*recs))
	}
}

func TestHookSender_PostToolUseTableDriven(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		toolName     string
		toolInput    any
		toolResponse any
		duration     int64
	}{
		{
			name:         "Bash",
			toolName:     "Bash",
			toolInput:    map[string]any{"command": "echo hi"},
			toolResponse: map[string]any{"stdout": "hi"},
			duration:     10,
		},
		{
			name:         "Read",
			toolName:     "Read",
			toolInput:    map[string]any{"file_path": "/tmp/x"},
			toolResponse: "file contents",
			duration:     0,
		},
		{
			name:         "EmptyResponse",
			toolName:     "Write",
			toolInput:    nil,
			toolResponse: nil,
			duration:     999999,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, recs, mu := captureServer(t)
			settings := settingsWithHooks(hookEventPostToolUse, nil, srv.URL+"/hooks/tool-use")
			sender := newTestSender(t, settings)
			if err := sender.OnToolUse(context.Background(), "tu", tc.toolName, tc.toolInput, tc.toolResponse, tc.duration); err != nil {
				t.Fatalf("OnToolUse: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(*recs) != 1 {
				t.Fatalf("expected 1 request, got %d", len(*recs))
			}
			rec := (*recs)[0]
			if rec.body["tool_name"] != tc.toolName {
				t.Errorf("tool_name = %v, want %v", rec.body["tool_name"], tc.toolName)
			}
			if rec.body["duration_ms"] != float64(tc.duration) {
				t.Errorf("duration_ms = %v, want %v", rec.body["duration_ms"], tc.duration)
			}
		})
	}
}
