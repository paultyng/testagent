package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// matchersFor builds a single-event matcher map with one HTTP hook per URL.
// Headers are applied to every hook.
func matchersFor(event string, headers map[string]string, urls ...string) map[string][]Matcher {
	hooks := make([]Hook, 0, len(urls))
	for _, u := range urls {
		hooks = append(hooks, Hook{Type: "http", URL: u, Headers: headers})
	}
	return map[string][]Matcher{
		event: {{Matcher: "*", Hooks: hooks}},
	}
}

func newTestSender(t *testing.T, matchers map[string][]Matcher) *Sender {
	t.Helper()
	return NewSender(matchers, "session-xyz", "/tmp/cwd", "/tmp/transcript.jsonl", "auto", nil)
}

// newCmdTestSender is the command-hook analog of newTestSender. Command
// hooks chdir into the configured cwd before spawning the shell, so the
// path must actually exist — t.TempDir gives each test a fresh real dir.
func newCmdTestSender(t *testing.T, matchers map[string][]Matcher) *Sender {
	t.Helper()
	return NewSender(matchers, "session-xyz", t.TempDir(), "/tmp/transcript.jsonl", "auto", nil)
}

func TestSender_NilMatchers_NoOp(t *testing.T) {
	t.Parallel()
	sender := newTestSender(t, nil)
	ctx := context.Background()
	if err := sender.OnPrompt(ctx, "hi", "title"); err != nil {
		t.Errorf("OnPrompt: %v", err)
	}
	if err := sender.OnPostToolUse(ctx, "tu_1", "Bash", map[string]any{"cmd": "ls"}, "ok", 5); err != nil {
		t.Errorf("OnToolUse: %v", err)
	}
	if err := sender.OnStop(ctx, "bye", false); err != nil {
		t.Errorf("OnStop: %v", err)
	}
	if err := sender.OnSessionEnd(ctx, "clear"); err != nil {
		t.Errorf("OnSessionEnd: %v", err)
	}
}

func TestSender_NoMatchingEvent_NoOp(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	// Matchers register Stop only — OnPrompt should be a no-op.
	sender := newTestSender(t, matchersFor(Stop, nil, srv.URL+"/hooks/stop"))
	if err := sender.OnPrompt(context.Background(), "hi", "title"); err != nil {
		t.Fatalf("OnPrompt: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 0 {
		t.Errorf("expected no requests, got %d", len(*recs))
	}
}

func TestSender_OnPrompt_PayloadAndHeaders(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	headers := map[string]string{"X-Session-Id": "abc-123"}
	sender := newTestSender(t, matchersFor(UserPromptSubmit, headers, srv.URL+"/hooks/prompt"))

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

func TestSender_OnPreToolUse_Payload(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	sender := newTestSender(t, matchersFor(PreToolUse, nil, srv.URL+"/hooks/pre-tool-use"))

	toolInput := map[string]any{"path": "main.go"}
	if _, err := sender.OnPreToolUse(context.Background(), "tu_pre", "read_file", toolInput); err != nil {
		t.Fatalf("OnPreToolUse: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	if got := rec.body["hook_event_name"]; got != "PreToolUse" {
		t.Errorf("hook_event_name = %v, want PreToolUse", got)
	}
	if got := rec.body["tool_name"]; got != "read_file" {
		t.Errorf("tool_name = %v, want read_file", got)
	}
	if got := rec.body["tool_use_id"]; got != "tu_pre" {
		t.Errorf("tool_use_id = %v, want tu_pre", got)
	}
	gotInput, ok := rec.body["tool_input"].(map[string]any)
	if !ok || gotInput["path"] != "main.go" {
		t.Errorf("tool_input = %v, want {path:main.go}", rec.body["tool_input"])
	}
	// Pre body must not carry tool_response or duration_ms.
	if _, ok := rec.body["tool_response"]; ok {
		t.Errorf("PreToolUse body must not include tool_response, got %v", rec.body["tool_response"])
	}
	if _, ok := rec.body["duration_ms"]; ok {
		t.Errorf("PreToolUse body must not include duration_ms, got %v", rec.body["duration_ms"])
	}
}

func TestSender_OnToolUse_PayloadAndHeaders(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	sender := newTestSender(t, matchersFor(PostToolUse, map[string]string{"X-Session-Slug": "demo"}, srv.URL+"/hooks/tool-use"))

	toolInput := map[string]any{"command": "ls -la"}
	toolResponse := map[string]any{"stdout": "file1\nfile2", "exit_code": float64(0)}
	if err := sender.OnPostToolUse(context.Background(), "tu_42", "Bash", toolInput, toolResponse, 1234); err != nil {
		t.Fatalf("OnToolUse: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	if got := rec.headers.Get("X-Session-Slug"); got != "demo" {
		t.Errorf("X-Session-Slug = %q, want demo", got)
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

func TestSender_OnStop_Payload(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	sender := newTestSender(t, matchersFor(Stop, nil, srv.URL+"/hooks/stop"))

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

func TestSender_OnSessionStart_Payload(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	sender := newTestSender(t, matchersFor(SessionStart, nil, srv.URL+"/hooks/start"))

	if err := sender.OnSessionStart(context.Background(), "startup"); err != nil {
		t.Fatalf("OnSessionStart: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	wantFields := map[string]any{
		"cwd":             "/tmp/cwd",
		"hook_event_name": "SessionStart",
		"source":          "startup",
		"session_id":      "session-xyz",
		"transcript_path": "/tmp/transcript.jsonl",
	}
	for k, want := range wantFields {
		if got := rec.body[k]; got != want {
			t.Errorf("body[%s] = %v, want %v", k, got, want)
		}
	}
}

func TestSender_OnSessionEnd_Payload(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	sender := newTestSender(t, matchersFor(SessionEnd, nil, srv.URL+"/hooks/end"))

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

func TestSender_OnPreCompact_Payload(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	sender := newTestSender(t, matchersFor(PreCompact, nil, srv.URL+"/hooks/pre-compact"))

	if err := sender.OnPreCompact(context.Background(), "manual"); err != nil {
		t.Fatalf("OnPreCompact: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	wantFields := map[string]any{
		"cwd":             "/tmp/cwd",
		"hook_event_name": "PreCompact",
		"trigger":         "manual",
		"session_id":      "session-xyz",
		"transcript_path": "/tmp/transcript.jsonl",
	}
	for k, want := range wantFields {
		if got := rec.body[k]; got != want {
			t.Errorf("body[%s] = %v, want %v", k, got, want)
		}
	}
}

func TestSender_OnPostCompact_Payload(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	sender := newTestSender(t, matchersFor(PostCompact, nil, srv.URL+"/hooks/post-compact"))

	if err := sender.OnPostCompact(context.Background(), "auto"); err != nil {
		t.Fatalf("OnPostCompact: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	wantFields := map[string]any{
		"hook_event_name": "PostCompact",
		"trigger":         "auto",
	}
	for k, want := range wantFields {
		if got := rec.body[k]; got != want {
			t.Errorf("body[%s] = %v, want %v", k, got, want)
		}
	}
}

func TestSender_OnNotification_Payload(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	sender := newTestSender(t, matchersFor(Notification, nil, srv.URL+"/hooks/notification"))

	if err := sender.OnNotification(context.Background(), NotificationPermissionPrompt, "approve Bash?", "Bash"); err != nil {
		t.Fatalf("OnNotification: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recs))
	}
	rec := (*recs)[0]
	wantFields := map[string]any{
		"hook_event_name": "Notification",
		"matcher":         "permission_prompt",
		"message":         "approve Bash?",
		"title":           "Bash",
	}
	for k, want := range wantFields {
		if got := rec.body[k]; got != want {
			t.Errorf("body[%s] = %v, want %v", k, got, want)
		}
	}
}

func TestSender_OnPermissionRequest_PayloadAndAllow(t *testing.T) {
	t.Parallel()
	srv := decideServer(t, `{"hookSpecificOutput":{"decision":{"behavior":"allow"}}}`)
	sender := newTestSender(t, matchersFor(PermissionRequest, nil, srv.URL))

	res, err := sender.OnPermissionRequest(context.Background(), "tu_1", "Bash", map[string]any{"command": "ls"})
	if err != nil {
		t.Fatalf("OnPermissionRequest: %v", err)
	}
	if !res.Allow || res.Block || res.Ask {
		t.Errorf("decision = %+v, want Allow=true only", res)
	}
}

func TestSender_OnPermissionRequest_DenyWithMessage(t *testing.T) {
	t.Parallel()
	srv := decideServer(t, `{"hookSpecificOutput":{"decision":{"behavior":"deny","message":"timed out"}}}`)
	sender := newTestSender(t, matchersFor(PermissionRequest, nil, srv.URL))

	res, err := sender.OnPermissionRequest(context.Background(), "tu_1", "Bash", nil)
	if err != nil {
		t.Fatalf("OnPermissionRequest: %v", err)
	}
	if !res.Block || res.Reason != "timed out" {
		t.Errorf("decision = %+v, want Block=true Reason=%q", res, "timed out")
	}
}

func TestSender_OnPermissionRequest_AggregatesLastAllowWins(t *testing.T) {
	t.Parallel()
	// Two matchers — both allow, second's reason should carry per the
	// PermissionRequest aggregation rule.
	first := decideServer(t, `{"hookSpecificOutput":{"decision":{"behavior":"allow","message":"first"}}}`)
	second := decideServer(t, `{"hookSpecificOutput":{"decision":{"behavior":"allow","message":"second"}}}`)
	matchers := map[string][]Matcher{
		PermissionRequest: {
			{Matcher: "*", Hooks: []Hook{{Type: "http", URL: first.URL}}},
			{Matcher: "*", Hooks: []Hook{{Type: "http", URL: second.URL}}},
		},
	}
	sender := newTestSender(t, matchers)

	res, err := sender.OnPermissionRequest(context.Background(), "tu_1", "Bash", nil)
	if err != nil {
		t.Fatalf("OnPermissionRequest: %v", err)
	}
	if !res.Allow || res.Reason != "second" {
		t.Errorf("aggregate = %+v, want Allow=true Reason=%q", res, "second")
	}
}

func TestSender_OnPermissionRequest_AggregatesAnyDenyWins(t *testing.T) {
	t.Parallel()
	allow := decideServer(t, `{"hookSpecificOutput":{"decision":{"behavior":"allow"}}}`)
	deny := decideServer(t, `{"hookSpecificOutput":{"decision":{"behavior":"deny","message":"second denies"}}}`)
	tail := decideServer(t, `{"hookSpecificOutput":{"decision":{"behavior":"allow","message":"third allows"}}}`)
	matchers := map[string][]Matcher{
		PermissionRequest: {
			{Matcher: "*", Hooks: []Hook{{Type: "http", URL: allow.URL}}},
			{Matcher: "*", Hooks: []Hook{{Type: "http", URL: deny.URL}}},
			{Matcher: "*", Hooks: []Hook{{Type: "http", URL: tail.URL}}},
		},
	}
	sender := newTestSender(t, matchers)

	res, err := sender.OnPermissionRequest(context.Background(), "tu_1", "Bash", nil)
	if err != nil {
		t.Fatalf("OnPermissionRequest: %v", err)
	}
	if !res.Block || res.Reason != "second denies" {
		t.Errorf("aggregate = %+v, want Block=true Reason=%q", res, "second denies")
	}
}

func TestSender_OnPermissionRequest_DefaultTimeout(t *testing.T) {
	t.Parallel()
	// permissionRequestTimeout is the per-event default. defaultTimeoutFor
	// should return the longer ceiling for this event, the standard 10s
	// elsewhere — verify both branches.
	if got, want := defaultTimeoutFor(PermissionRequest), permissionRequestTimeout; got != want {
		t.Errorf("defaultTimeoutFor(PermissionRequest) = %s, want %s", got, want)
	}
	if got, want := defaultTimeoutFor(Notification), defaultTimeout; got != want {
		t.Errorf("defaultTimeoutFor(Notification) = %s, want %s", got, want)
	}
}

func TestSender_MultipleHooksFire(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	// Two HTTP hooks under one matcher, plus a second matcher with a third hook.
	matchers := map[string][]Matcher{
		UserPromptSubmit: {
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
	}
	sender := newTestSender(t, matchers)
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

func TestSender_UnknownHookTypeSkipped(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	matchers := map[string][]Matcher{
		Stop: {
			{
				Matcher: "*",
				Hooks: []Hook{
					{Type: "webhook", URL: "shouldnotfire"},
					{Type: "http", URL: srv.URL + "/hooks/stop"},
				},
			},
		},
	}
	sender := newTestSender(t, matchers)
	if err := sender.OnStop(context.Background(), "msg", false); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Errorf("expected 1 http request, got %d", len(*recs))
	}
}

func TestSender_OneHookFailingDoesNotBlockOthers(t *testing.T) {
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

	matchers := map[string][]Matcher{
		Stop: {
			{
				Matcher: "*",
				Hooks: []Hook{
					{Type: "http", URL: badSrv.URL + "/bad"},
					{Type: "http", URL: goodSrv.URL + "/good"},
				},
			},
		},
	}
	sender := newTestSender(t, matchers)
	err := sender.OnStop(context.Background(), "msg", false)
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	if got := atomic.LoadInt32(&goodHits); got != 1 {
		t.Errorf("good server hits = %d, want 1 (failing hook should not block others)", got)
	}
}

func TestSender_HookTimeoutHonored(t *testing.T) {
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

	matchers := map[string][]Matcher{
		Stop: {{
			Matcher: "*",
			Hooks:   []Hook{{Type: "http", URL: srv.URL, Timeout: 1}}, // 1 second
		}},
	}
	sender := newTestSender(t, matchers)
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

func TestSender_DefaultTimeoutWhenZero(t *testing.T) {
	t.Parallel()
	// Hook.Timeout=0 should fall back to defaultTimeout, not be zero.
	// We assert behavior by checking the request reaches the server quickly.
	srv, recs, mu := captureServer(t)
	matchers := map[string][]Matcher{
		SessionEnd: {{
			Matcher: "*",
			Hooks:   []Hook{{Type: "http", URL: srv.URL, Timeout: 0}},
		}},
	}
	sender := newTestSender(t, matchers)
	if err := sender.OnSessionEnd(context.Background(), "clear"); err != nil {
		t.Fatalf("OnSessionEnd: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*recs) != 1 {
		t.Errorf("expected 1 request, got %d", len(*recs))
	}
}

func TestSender_PostToolUseTableDriven(t *testing.T) {
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
			sender := newTestSender(t, matchersFor(PostToolUse, nil, srv.URL+"/hooks/tool-use"))
			if err := sender.OnPostToolUse(context.Background(), "tu", tc.toolName, tc.toolInput, tc.toolResponse, tc.duration); err != nil {
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

func TestSender_Verbose_EmitsLinePerHook(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var dbg bytes.Buffer
	// No per-hook Timeout — the 1s value previously here flaked when
	// CI scheduled this test alongside many other httptest.NewServer
	// callers and the local POST+200 round-trip occasionally crossed
	// the 1s ceiling. Default 10s is plenty for a localhost handler.
	matchers := map[string][]Matcher{
		Stop: {
			{Hooks: []Hook{
				{Type: "http", URL: srv.URL + "/a"},
				{Type: "http", URL: srv.URL + "/b"},
			}},
		},
	}
	h := NewSender(matchers, "sid", "/tmp", "", "default", &dbg)
	if err := h.OnStop(context.Background(), "msg", false); err != nil {
		t.Fatalf("OnStop: %v", err)
	}

	lines := strings.Split(strings.TrimRight(dbg.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d debug lines, want 2:\n%s", len(lines), dbg.String())
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "hook Stop POST ") {
			t.Errorf("line missing prefix: %q", l)
		}
		if !strings.Contains(l, " 200 ") {
			t.Errorf("line missing 200 status: %q", l)
		}
	}
}

func TestSender_Verbose_RecordsErrorStatus(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	var dbg bytes.Buffer
	matchers := map[string][]Matcher{
		Stop: {{Hooks: []Hook{{Type: "http", URL: srv.URL}}}},
	}
	h := NewSender(matchers, "sid", "/tmp", "", "default", &dbg)
	_ = h.OnStop(context.Background(), "msg", false)

	out := dbg.String()
	if !strings.Contains(out, " 500 ") {
		t.Errorf("verbose line missing 500 status: %q", out)
	}
	if !strings.Contains(out, `err="status 500"`) {
		t.Errorf("verbose line missing err snippet: %q", out)
	}
}

func TestSender_Verbose_RecordsTransportError(t *testing.T) {
	t.Parallel()

	var dbg bytes.Buffer
	matchers := map[string][]Matcher{
		Stop: {{Hooks: []Hook{{Type: "http", URL: "http://127.0.0.1:1", Timeout: 1}}}},
	}
	h := NewSender(matchers, "sid", "/tmp", "", "default", &dbg)
	_ = h.OnStop(context.Background(), "msg", false)

	out := dbg.String()
	if !strings.Contains(out, " ERR ") {
		t.Errorf("verbose line missing ERR token: %q", out)
	}
	if !strings.Contains(out, `err="posting:`) {
		t.Errorf("verbose line missing err snippet: %q", out)
	}
}

func TestSender_Verbose_DisabledByDefault(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	matchers := map[string][]Matcher{
		Stop: {{Hooks: []Hook{{Type: "http", URL: srv.URL}}}},
	}
	// debugWriter == nil → no output collected anywhere; this test asserts
	// the no-debug path doesn't panic and behaves like before.
	h := NewSender(matchers, "sid", "/tmp", "", "default", nil)
	if err := h.OnStop(context.Background(), "msg", false); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
}

// stdinToFileCmd returns a shell command string that writes stdin to
// outPath. Portable across `$SHELL -lc` (Unix: `cat`) and `cmd.exe /C`
// (Windows: `findstr "."` — reads stdin, matches every non-empty line,
// writes to the redirect target). t.TempDir paths on Windows are
// space-free so the unquoted form is safe — see codexhooks's
// writeTwoLineCmd note.
func stdinToFileCmd(outPath string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`findstr "." > %s`, outPath)
	}
	return fmt.Sprintf("cat > %q", outPath)
}

// hookCmdTimeout is the per-hook Timeout (seconds) used by command-hook
// tests that assert side effects. Generous to absorb $SHELL -lc init
// slack on slow CI runners — see codexhooks's hookTestTimeout note.
const hookCmdTimeout = 30

func TestSender_CommandHook_StdinReceivesPayload(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "stop.json")
	matchers := map[string][]Matcher{
		Stop: {
			{
				Matcher: "*",
				Hooks: []Hook{
					{Type: "command", Command: stdinToFileCmd(out), Timeout: hookCmdTimeout},
				},
			},
		},
	}
	sender := newCmdTestSender(t, matchers)
	if err := sender.OnStop(context.Background(), "hello", true); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &body); err != nil {
		t.Fatalf("unmarshal sentinel (raw=%q): %v", raw, err)
	}
	if got := body["hook_event_name"]; got != Stop {
		t.Errorf("hook_event_name = %v, want %q", got, Stop)
	}
	if got := body["last_assistant_message"]; got != "hello" {
		t.Errorf("last_assistant_message = %v, want %q", got, "hello")
	}
	if got := body["stop_hook_active"]; got != true {
		t.Errorf("stop_hook_active = %v, want true", got)
	}
}

func TestSender_CommandHook_CwdHonored(t *testing.T) {
	t.Parallel()
	// Use a relative output path; the shell only resolves it correctly if
	// the hook is spawned with cmd.Dir == s.cwd. Regression guard for the
	// missing-cwd bug surfaced in PR #65 review.
	dir := t.TempDir()
	matchers := map[string][]Matcher{
		Stop: {
			{
				Matcher: "*",
				Hooks: []Hook{
					{Type: "command", Command: stdinToFileCmd("stop.json"), Timeout: hookCmdTimeout},
				},
			},
		},
	}
	sender := NewSender(matchers, "session-xyz", dir, "/tmp/t.jsonl", "auto", nil)
	if err := sender.OnStop(context.Background(), "msg", false); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stop.json")); err != nil {
		t.Errorf("sentinel missing at sender cwd: %v", err)
	}
}

func TestSender_MixedHTTPAndCommand(t *testing.T) {
	t.Parallel()
	srv, recs, mu := captureServer(t)
	out := filepath.Join(t.TempDir(), "prompt.json")
	matchers := map[string][]Matcher{
		UserPromptSubmit: {
			{
				Matcher: "*",
				Hooks: []Hook{
					{Type: "http", URL: srv.URL + "/hooks/prompt"},
					{Type: "command", Command: stdinToFileCmd(out), Timeout: hookCmdTimeout},
				},
			},
		},
	}
	sender := newCmdTestSender(t, matchers)
	if err := sender.OnPrompt(context.Background(), "hi", "title"); err != nil {
		t.Fatalf("OnPrompt: %v", err)
	}
	mu.Lock()
	httpCount := len(*recs)
	mu.Unlock()
	if httpCount != 1 {
		t.Errorf("http hits = %d, want 1", httpCount)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("command-hook sentinel missing: %v", err)
	}
}

func TestSender_CommandHook_TimeoutHonored(t *testing.T) {
	t.Parallel()
	// Hook command sleeps well past its 1s Timeout. Assert the call
	// returns an error within a few seconds (not the full sleep wall-clock).
	var sleepCmd string
	if runtime.GOOS == "windows" {
		sleepCmd = "ping -n 10 127.0.0.1 >NUL"
	} else {
		sleepCmd = "sleep 10"
	}
	matchers := map[string][]Matcher{
		Stop: {
			{
				Matcher: "*",
				Hooks: []Hook{
					{Type: "command", Command: sleepCmd, Timeout: 1},
				},
			},
		},
	}
	// Use os.TempDir() rather than t.TempDir() for the cwd: the test
	// force-kills the shell on timeout, and on Windows the kill doesn't
	// release the dir's file handles in time for t.TempDir's cleanup,
	// which then fails the test. The shell here doesn't write to cwd, so
	// the global tmp dir is a safe stand-in that needs no cleanup.
	sender := NewSender(matchers, "session-xyz", os.TempDir(), "/tmp/transcript.jsonl", "auto", nil)
	start := time.Now()
	err := sender.OnStop(context.Background(), "msg", false)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %s, want < 5s (timeout cancel path)", elapsed)
	}
}

func TestSender_CommandHook_DebugLine(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "out.json")
	matchers := map[string][]Matcher{
		SessionStart: {
			{
				Matcher: "*",
				Hooks: []Hook{
					{Type: "command", Command: stdinToFileCmd(out), Timeout: hookCmdTimeout},
				},
			},
		},
	}
	var buf bytes.Buffer
	sender := NewSender(matchers, "sid", t.TempDir(), "/tmp/t.jsonl", "auto", &buf)
	if err := sender.OnSessionStart(context.Background(), "startup"); err != nil {
		t.Fatalf("OnSessionStart: %v", err)
	}
	line := buf.String()
	for _, want := range []string{"hook SessionStart CMD", "OK"} {
		if !strings.Contains(line, want) {
			t.Errorf("debug line missing %q\nfull line: %s", want, line)
		}
	}
}

// decideServer is a captureServer variant that responds with a caller-
// supplied body. Lets PreToolUse / PermissionRequest tests assert that
// the response decision lands on the returned hookresult.Result.
func decideServer(t *testing.T, responseBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, responseBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSender_OnPreToolUse_ParsesAllowResponse(t *testing.T) {
	t.Parallel()
	srv := decideServer(t, `{"hookSpecificOutput":{"permissionDecision":"allow"}}`)
	sender := newTestSender(t, matchersFor(PreToolUse, nil, srv.URL))

	res, err := sender.OnPreToolUse(context.Background(), "tu_1", "Bash", map[string]any{"command": "ls"})
	if err != nil {
		t.Fatalf("OnPreToolUse: %v", err)
	}
	if res.Block || res.Ask {
		t.Errorf("unexpected decision: %+v", res)
	}
	if !res.Allow {
		t.Errorf("Allow = false, want true; got %+v", res)
	}
}

func TestSender_OnPreToolUse_ParsesDenyResponse(t *testing.T) {
	t.Parallel()
	srv := decideServer(t, `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"not in allowlist"}}`)
	sender := newTestSender(t, matchersFor(PreToolUse, nil, srv.URL))

	res, err := sender.OnPreToolUse(context.Background(), "tu_1", "Bash", map[string]any{"command": "rm -rf /"})
	if err != nil {
		t.Fatalf("OnPreToolUse: %v", err)
	}
	if !res.Block {
		t.Errorf("Block = false, want true; got %+v", res)
	}
	if res.Reason != "not in allowlist" {
		t.Errorf("Reason = %q, want %q", res.Reason, "not in allowlist")
	}
}

func TestSender_OnPreToolUse_AggregatesAcrossMatchers(t *testing.T) {
	t.Parallel()
	// Two matchers: first allows, second denies. PreToolUse aggregation
	// rule = any-block-wins.
	allow := decideServer(t, `{"hookSpecificOutput":{"permissionDecision":"allow"}}`)
	deny := decideServer(t, `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"second matcher denies"}}`)
	matchers := map[string][]Matcher{
		PreToolUse: {
			{Matcher: "*", Hooks: []Hook{{Type: "http", URL: allow.URL}}},
			{Matcher: "*", Hooks: []Hook{{Type: "http", URL: deny.URL}}},
		},
	}
	sender := newTestSender(t, matchers)

	res, err := sender.OnPreToolUse(context.Background(), "tu_1", "Bash", nil)
	if err != nil {
		t.Fatalf("OnPreToolUse: %v", err)
	}
	if !res.Block || res.Reason != "second matcher denies" {
		t.Errorf("aggregate = %+v, want Block=true Reason=%q", res, "second matcher denies")
	}
}

func TestSender_OnPreToolUse_CommandHookExitCode2Blocks(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		// cmd.exe quote-escaping interacts poorly with stderr redirect
		// + exit 2 inside a single command string. The parse/aggregate
		// path is cross-platform tested in internal/hookresult; this
		// integration test asserts the runner correctly wires stdout/
		// stderr/exitcode into ParseCommand on Unix.
		t.Skip("windows cmd.exe stderr redirect + exit 2 quoting")
	}
	matchers := map[string][]Matcher{
		PreToolUse: {
			{Matcher: "*", Hooks: []Hook{{Type: "command", Command: `printf 'blocked: dangerous\n' 1>&2; exit 2`, Timeout: hookCmdTimeout}}},
		},
	}
	sender := newCmdTestSender(t, matchers)

	res, err := sender.OnPreToolUse(context.Background(), "tu_1", "Bash", nil)
	if err != nil {
		t.Fatalf("OnPreToolUse: %v", err)
	}
	if !res.Block {
		t.Errorf("Block = false, want true; got %+v", res)
	}
	if !strings.Contains(res.Reason, "blocked: dangerous") {
		t.Errorf("Reason = %q, want substring %q", res.Reason, "blocked: dangerous")
	}
}

func TestSender_OnPreToolUse_CommandHookExit0ParsesStdout(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		// JSON braces and quotes in a single echo'd argument blow up
		// cmd.exe quote rules. Parser is covered cross-platform by
		// internal/hookresult.
		t.Skip("windows cmd.exe quoting for JSON literal")
	}
	matchers := map[string][]Matcher{
		PreToolUse: {
			{Matcher: "*", Hooks: []Hook{{Type: "command", Command: `printf '{"hookSpecificOutput":{"permissionDecision":"allow"}}\n'`, Timeout: hookCmdTimeout}}},
		},
	}
	sender := newCmdTestSender(t, matchers)

	res, err := sender.OnPreToolUse(context.Background(), "tu_1", "Bash", nil)
	if err != nil {
		t.Fatalf("OnPreToolUse: %v", err)
	}
	if !res.Allow || res.Block || res.Ask {
		t.Errorf("decision = %+v, want Allow=true only", res)
	}
}

func TestMatchesMatcher(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern, value string
		want           bool
	}{
		// Catch-all.
		{"", "Bash", true},
		{"*", "Bash", true},
		{"", "", true},
		// Exact.
		{"Bash", "Bash", true},
		{"Bash", "bash", false},
		{"Bash", "Read", false},
		// Alternation.
		{"Read|Edit|MultiEdit", "Read", true},
		{"Read|Edit|MultiEdit", "Edit", true},
		{"Read|Edit|MultiEdit", "MultiEdit", true},
		{"Read|Edit|MultiEdit", "Bash", false},
		{"Read|Edit|MultiEdit", "read", false},
		// Regex (unanchored substring).
		{"^Bash$", "Bash", true},
		{"^Bash$", "Bashful", false},
		{"^B.*", "Bash", true},
		{"^B.*", "Read", false},
		// Pattern with both `|` and regex metacharacters → regex, not alternation.
		{"A|B[0-9]", "B5", true},
		{"A|B[0-9]", "A", true},
		{"A|B[0-9]", "C", false},
		// Malformed regex → no match (not panic).
		{"[", "Bash", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"_vs_"+tc.value, func(t *testing.T) {
			t.Parallel()
			if got := matchesMatcher(tc.pattern, tc.value); got != tc.want {
				t.Errorf("matchesMatcher(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
			}
		})
	}
}

func TestSender_OnPreToolUse_MatcherFiltersByToolName(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	matchers := map[string][]Matcher{
		PreToolUse: {
			{Matcher: "Bash", Hooks: []Hook{{Type: "http", URL: srv.URL}}},
		},
	}
	sender := newTestSender(t, matchers)

	// Read does NOT match "Bash" — hook must not fire.
	if _, err := sender.OnPreToolUse(context.Background(), "tu_1", "Read", nil); err != nil {
		t.Fatalf("OnPreToolUse Read: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("Bash-matcher fired for Read tool; hits=%d, want 0", got)
	}

	// Bash matches — hook fires.
	if _, err := sender.OnPreToolUse(context.Background(), "tu_2", "Bash", nil); err != nil {
		t.Fatalf("OnPreToolUse Bash: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("Bash-matcher missed Bash tool; hits=%d, want 1", got)
	}
}

func TestSender_OnPreToolUse_MatcherAlternation(t *testing.T) {
	t.Parallel()
	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen = append(seen, body["tool_name"].(string))
		mu.Unlock()
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	matchers := map[string][]Matcher{
		PreToolUse: {
			{Matcher: "Read|Edit", Hooks: []Hook{{Type: "http", URL: srv.URL}}},
		},
	}
	sender := newTestSender(t, matchers)

	for _, tool := range []string{"Read", "Edit", "Bash", "MultiEdit"} {
		if _, err := sender.OnPreToolUse(context.Background(), "tu", tool, nil); err != nil {
			t.Fatalf("OnPreToolUse %s: %v", tool, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"Read", "Edit"}
	if len(seen) != len(want) {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("seen[%d] = %q, want %q", i, seen[i], w)
		}
	}
}

func TestSender_OnNotification_MatcherFiltersBySubtype(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	matchers := map[string][]Matcher{
		Notification: {
			{Matcher: NotificationPermissionPrompt, Hooks: []Hook{{Type: "http", URL: srv.URL}}},
		},
	}
	sender := newTestSender(t, matchers)

	// idle_prompt should NOT match a permission_prompt matcher.
	if err := sender.OnNotification(context.Background(), NotificationIdlePrompt, "msg", ""); err != nil {
		t.Fatalf("OnNotification idle: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("permission_prompt-matcher fired for idle_prompt; hits=%d, want 0", got)
	}

	// permission_prompt matches.
	if err := sender.OnNotification(context.Background(), NotificationPermissionPrompt, "msg", ""); err != nil {
		t.Fatalf("OnNotification permission: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("permission_prompt-matcher missed permission_prompt; hits=%d, want 1", got)
	}
}

func TestSender_NonToolEvent_IgnoresMatcher(t *testing.T) {
	t.Parallel()
	// Stop has no scoping axis — every matcher should fire regardless of
	// its pattern. Verify Stop with a "Bash" matcher (which would filter
	// out everything for a tool-scoped event) still fires.
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	matchers := map[string][]Matcher{
		Stop: {{Matcher: "Bash", Hooks: []Hook{{Type: "http", URL: srv.URL}}}},
	}
	sender := newTestSender(t, matchers)

	if err := sender.OnStop(context.Background(), "done", false); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("non-tool event filtered by matcher; hits=%d, want 1", got)
	}
}

func TestFormatBytes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		n    int
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0kB"},
		{2560, "2.5kB"},
		{1024 * 1024, "1.0MB"},
		{1024 * 1024 * 3, "3.0MB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.n); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
