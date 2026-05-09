package slash

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

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
)

func newTestHandler(out *bytes.Buffer) *Handler {
	return New(0, hooks.NewSender(nil, "sid-test", "/tmp", "", "default", nil), mcp.NewClient(nil), out)
}

func TestSlash_NotASlash(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestHandler(out)
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
	h := newTestHandler(out)
	h.Dispatch(context.Background(), "/stream hello world")
	got := strings.TrimRight(out.String(), "\n")
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

// TestSlash_Think asserts cmdThink populates Outcome.Prompt and
// ThinkDuration correctly. The actual hook firing + animation is the
// caller's responsibility (main.go scanner loop, tui.go Update) and is
// covered by TestE2E_*.
func TestSlash_Think(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, line string
		wantPrompt string
		wantDur    time.Duration
		wantHasDur bool
	}{
		{name: "text only", line: "/think pondering deeply", wantPrompt: "pondering deeply"},
		{name: "duration + text", line: "/think 5s working", wantPrompt: "working", wantDur: 5 * time.Second, wantHasDur: true},
		{name: "non-duration first token", line: "/think 5seconds working", wantPrompt: "5seconds working"},
		{name: "duration only (no message)", line: "/think 1h", wantDur: time.Hour, wantHasDur: true},
		{name: "explicit zero — instant echo", line: "/think 0 done", wantPrompt: "done", wantDur: 0, wantHasDur: true},
		{name: "bare /think — bare prompt with default duration", line: "/think"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h := newTestHandler(out)
			outcome := h.Dispatch(context.Background(), tc.line)

			if !outcome.Handled {
				t.Errorf("Handled = false, want true")
			}
			if outcome.Prompt != tc.wantPrompt {
				t.Errorf("Prompt = %q, want %q", outcome.Prompt, tc.wantPrompt)
			}
			if outcome.ThinkDuration != tc.wantDur {
				t.Errorf("ThinkDuration = %v, want %v", outcome.ThinkDuration, tc.wantDur)
			}
			if outcome.HasThinkDuration != tc.wantHasDur {
				t.Errorf("HasThinkDuration = %v, want %v", outcome.HasThinkDuration, tc.wantHasDur)
			}
			// cmdThink doesn't render directly — the caller does.
			if out.Len() != 0 {
				t.Errorf("cmdThink wrote to out (should be caller's responsibility): %q", out.String())
			}
		})
	}
}

func TestParseThinkArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in          string
		wantDur     time.Duration
		wantHasExpl bool
		wantMsg     string
	}{
		{in: "5s working on it", wantDur: 5 * time.Second, wantHasExpl: true, wantMsg: "working on it"},
		{in: "200ms quick", wantDur: 200 * time.Millisecond, wantHasExpl: true, wantMsg: "quick"},
		{in: "working on it", wantDur: 0, wantHasExpl: false, wantMsg: "working on it"},
		{in: "5seconds working", wantDur: 0, wantHasExpl: false, wantMsg: "5seconds working"},
		{in: "5s", wantDur: 5 * time.Second, wantHasExpl: true, wantMsg: ""},
		{in: "", wantDur: 0, wantHasExpl: false, wantMsg: ""},
		{in: "1h", wantDur: time.Hour, wantHasExpl: true, wantMsg: ""},
		{in: "0 done", wantDur: 0, wantHasExpl: true, wantMsg: "done"}, // explicit zero — caller treats as "instant"
		{in: "-5s clamped", wantDur: 0, wantHasExpl: true, wantMsg: "clamped"},
		{in: "  10ms  padded", wantDur: 10 * time.Millisecond, wantHasExpl: true, wantMsg: "padded"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			req := parseThinkArgs(tc.in)
			if req.Duration != tc.wantDur || req.HasExplicit != tc.wantHasExpl || req.Message != tc.wantMsg {
				t.Errorf("parseThinkArgs(%q) = {%v, %v, %q}, want {%v, %v, %q}",
					tc.in, req.Duration, req.HasExplicit, req.Message,
					tc.wantDur, tc.wantHasExpl, tc.wantMsg)
			}
		})
	}
}

func TestSlash_Help_Format(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.Dispatch(context.Background(), "/help")
	body := out.String()

	wantPhrases := []string{
		"/think [<duration>]",      // duration-aware /think advertised
		"/fake-tool ",              // renamed from /tool
		"/fake-tool-result ",       // renamed from /result
		"/mcp-call ",               // renamed from /mcp to avoid collision with real Claude's /mcp
		"/restart [clear|compact]", // /restart command shape
		"connected MCP tool",       // /mcp-call's distinguishing phrasing
		"exits testagent",          // verb-led /exit description
		"prints this list",         // verb-led /help description
	}
	for _, p := range wantPhrases {
		if !strings.Contains(body, p) {
			t.Errorf("/help missing phrase %q\n--- /help body ---\n%s", p, body)
		}
	}
	if strings.Contains(body, "/md ") {
		t.Errorf("/help still references /md (should be dropped):\n%s", body)
	}
}

func TestSlash_Panel(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestHandler(out)
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

func TestSlash_FakeToolAlone_NoHookYet(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool read_file {"path":"foo.go"}`)

	if !strings.Contains(out.String(), "read_file") {
		t.Errorf("output missing tool name: %q", out.String())
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("/fake-tool alone fired %d hook(s); want 0 (paired with /fake-tool-result)", got)
	}
}

func TestSlash_FakeToolResultPair(t *testing.T) {
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
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool read_file {"path":"foo.go"}`)
	time.Sleep(2 * time.Millisecond) // ensure non-zero duration_ms
	h.Dispatch(context.Background(), `/fake-tool-result {"contents":"package foo"}`)

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

func TestSlash_OrphanFakeToolResult(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool-result {"orphan":true}`)

	if !strings.Contains(out.String(), "orphan") {
		t.Errorf("output missing result body: %q", out.String())
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("orphan /fake-tool-result fired %d hook(s); want 0", got)
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
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool dangling {}`)
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
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL, Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool first {}`)
	h.Dispatch(context.Background(), `/fake-tool second {}`)
	h.Dispatch(context.Background(), `/fake-tool-result {"ok":true}`)

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
	h := newTestHandler(out)
	h.Dispatch(context.Background(), `/fake-tool-result {"ok":true}`)
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
	h := newTestHandler(out)

	noCode := h.Dispatch(context.Background(), "/exit")
	if !noCode.Exit || noCode.ExitCode != 0 {
		t.Errorf("/exit got Exit=%v Code=%d, want true/0", noCode.Exit, noCode.ExitCode)
	}

	withCode := h.Dispatch(context.Background(), "/exit 7")
	if !withCode.Exit || withCode.ExitCode != 7 {
		t.Errorf("/exit 7 got Exit=%v Code=%d, want true/7", withCode.Exit, withCode.ExitCode)
	}
}

func TestSlash_Restart(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		line       string
		wantReason string
	}{
		{name: "default reason", line: "/restart", wantReason: "clear"},
		{name: "explicit clear", line: "/restart clear", wantReason: "clear"},
		{name: "explicit compact", line: "/restart compact", wantReason: "compact"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h := newTestHandler(out)
			oc := h.Dispatch(context.Background(), tc.line)
			if !oc.Handled || !oc.Restart {
				t.Fatalf("got Handled=%v Restart=%v, want both true", oc.Handled, oc.Restart)
			}
			if oc.RestartReason != tc.wantReason {
				t.Errorf("RestartReason = %q, want %q", oc.RestartReason, tc.wantReason)
			}
			if oc.Exit {
				t.Errorf("Exit = true, want false (/restart should not exit)")
			}
		})
	}
}

func TestSlash_UnknownCommand(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestHandler(out)
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
			h1 := newTestHandler(&bytes.Buffer{})
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
			h2 := newTestHandler(out)
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
