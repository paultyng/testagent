package slash

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	return New(hooks.NewSender(nil, "sid-test", "/tmp", "", "default", nil), mcp.NewClient(nil), out)
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

// TestSlash_Stream and TestSlash_Think both assert the duration-prefix
// parser. /stream sets Stream{Duration,HasStreamDuration}; /think sets the
// ThinkDuration pair. Both require a duration as the first token; missing
// or unparseable durations write a usage line and return Prompt="" so the
// caller treats the dispatch as a pure side effect.
func TestSlash_Stream(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, line string
		wantPrompt string
		wantDur    time.Duration
		wantHasDur bool
		wantUsage  bool // expect a usage line written to out
	}{
		{name: "duration + text", line: "/stream 50ms hello world", wantPrompt: "hello world", wantDur: 50 * time.Millisecond, wantHasDur: true},
		{name: "duration only (empty body)", line: "/stream 100ms", wantUsage: true},
		{name: "no duration", line: "/stream hello world", wantUsage: true},
		{name: "bad duration", line: "/stream 5seconds hello", wantUsage: true},
		{name: "negative clamps", line: "/stream -10ms quick", wantPrompt: "quick", wantDur: 0, wantHasDur: true},
		{name: "explicit zero", line: "/stream 0 immediate echo", wantPrompt: "immediate echo", wantDur: 0, wantHasDur: true},
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
			if outcome.StreamDuration != tc.wantDur {
				t.Errorf("StreamDuration = %v, want %v", outcome.StreamDuration, tc.wantDur)
			}
			if outcome.HasStreamDuration != tc.wantHasDur {
				t.Errorf("HasStreamDuration = %v, want %v", outcome.HasStreamDuration, tc.wantHasDur)
			}
			gotUsage := strings.Contains(out.String(), "usage: /stream")
			if gotUsage != tc.wantUsage {
				t.Errorf("usage written? got %v, want %v (out=%q)", gotUsage, tc.wantUsage, out.String())
			}
		})
	}
}

func TestSlash_Think(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, line string
		wantPrompt string
		wantDur    time.Duration
		wantHasDur bool
		wantUsage  bool
	}{
		{name: "duration + text", line: "/think 5s working", wantPrompt: "working", wantDur: 5 * time.Second, wantHasDur: true},
		{name: "duration only (empty body)", line: "/think 1h", wantUsage: true},
		{name: "no duration", line: "/think pondering deeply", wantUsage: true},
		{name: "bad duration", line: "/think 5seconds working", wantUsage: true},
		{name: "explicit zero — instant echo", line: "/think 0 done", wantPrompt: "done", wantDur: 0, wantHasDur: true},
		{name: "negative clamps", line: "/think -5s clamped", wantPrompt: "clamped", wantDur: 0, wantHasDur: true},
		{name: "bare /think", line: "/think", wantUsage: true},
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
			gotUsage := strings.Contains(out.String(), "usage: /think")
			if gotUsage != tc.wantUsage {
				t.Errorf("usage written? got %v, want %v (out=%q)", gotUsage, tc.wantUsage, out.String())
			}
		})
	}
}

func TestParseDurationPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		wantDur time.Duration
		wantMsg string
		wantOk  bool
	}{
		{in: "5s working on it", wantDur: 5 * time.Second, wantMsg: "working on it", wantOk: true},
		{in: "200ms quick", wantDur: 200 * time.Millisecond, wantMsg: "quick", wantOk: true},
		{in: "5s", wantDur: 5 * time.Second, wantMsg: "", wantOk: true},
		{in: "1h", wantDur: time.Hour, wantMsg: "", wantOk: true},
		{in: "0 done", wantDur: 0, wantMsg: "done", wantOk: true},
		{in: "-5s clamped", wantDur: 0, wantMsg: "clamped", wantOk: true},
		{in: "  10ms  padded", wantDur: 10 * time.Millisecond, wantMsg: "padded", wantOk: true},
		{in: "working on it", wantOk: false},
		{in: "5seconds working", wantOk: false},
		{in: "", wantOk: false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			d, msg, ok := parseDurationPrefix(tc.in)
			if d != tc.wantDur || msg != tc.wantMsg || ok != tc.wantOk {
				t.Errorf("parseDurationPrefix(%q) = (%v, %q, %v), want (%v, %q, %v)",
					tc.in, d, msg, ok, tc.wantDur, tc.wantMsg, tc.wantOk)
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
		"/think <duration>",  // duration-required /think advertised
		"/stream <duration>", // duration-required /stream advertised
		"/link <url>",        // OSC 8 link helper advertised
		"/clear",             // upstream-shape clear command
		"/compact",           // upstream-shape compact command
		"/fake-auto-compact", // emulation-only auto-compact trigger
		"/quit",              // alias of /exit (codex parity)
		"/fake-tool ",        // renamed from /tool
		"/fake-tool-result ", // renamed from /result
		"/mcp-call ",         // renamed from /mcp to avoid collision with real Claude's /mcp
		"connected MCP tool", // /mcp-call's distinguishing phrasing
		"exits testagent",    // verb-led /exit description
		"prints this list",   // verb-led /help description
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

// TestSlash_Link asserts /link emits the OSC 8 byte shape with URL +
// text, falls back to URL when text is omitted, and writes a usage line
// when URL is missing. Closes #24.
func TestSlash_Link(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		line      string
		wantSubs  []string
		wantNoHit []string
	}{
		{
			name:     "url + text",
			line:     "/link https://example.com see-here",
			wantSubs: []string{"\x1b]8;;https://example.com\x1b\\see-here\x1b]8;;\x1b\\"},
		},
		{
			name:     "url only — text falls back to url",
			line:     "/link https://example.com",
			wantSubs: []string{"\x1b]8;;https://example.com\x1b\\https://example.com\x1b]8;;\x1b\\"},
		},
		{
			name:     "multi-word text preserves spaces",
			line:     "/link https://example.com click here please",
			wantSubs: []string{"\x1b]8;;https://example.com\x1b\\click here please\x1b]8;;\x1b\\"},
		},
		{
			name:      "missing url — usage line",
			line:      "/link",
			wantSubs:  []string{"usage: /link"},
			wantNoHit: []string{"\x1b]8"},
		},
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
			s := out.String()
			for _, sub := range tc.wantSubs {
				if !strings.Contains(s, sub) {
					t.Errorf("output missing %q\n--- output ---\n%q", sub, s)
				}
			}
			for _, sub := range tc.wantNoHit {
				if strings.Contains(s, sub) {
					t.Errorf("output unexpectedly contains %q\n--- output ---\n%q", sub, s)
				}
			}
		})
	}
}

func TestSlash_FakeToolAlone_FiresPreOnly(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		hit []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		hit = append(hit, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PreToolUse":  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool read_file {"path":"foo.go"}`)

	if !strings.Contains(out.String(), "read_file") {
		t.Errorf("output missing tool name: %q", out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hit) != 1 {
		t.Fatalf("got %d hook calls, want 1 (PreToolUse only)", len(hit))
	}
	if got := hit[0]["hook_event_name"]; got != "PreToolUse" {
		t.Errorf("hook_event_name = %v, want PreToolUse", got)
	}
	if got := hit[0]["tool_name"]; got != "read_file" {
		t.Errorf("tool_name = %v, want read_file", got)
	}
	// Pre body must NOT carry tool_response or duration_ms.
	if _, ok := hit[0]["tool_response"]; ok {
		t.Errorf("PreToolUse body must not include tool_response, got %v", hit[0]["tool_response"])
	}
	if _, ok := hit[0]["duration_ms"]; ok {
		t.Errorf("PreToolUse body must not include duration_ms, got %v", hit[0]["duration_ms"])
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

// TestSlash_FakeToolCycle_PreBeforePost asserts the full /fake-tool +
// /fake-tool-result cycle fires PreToolUse before PostToolUse, with the
// shared tool_use_id linking the two events.
func TestSlash_FakeToolCycle_PreBeforePost(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		seq []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["__path"] = r.URL.Path
		mu.Lock()
		seq = append(seq, body)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PreToolUse":  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool read_file {"path":"foo.go"}`)
	h.Dispatch(context.Background(), `/fake-tool-result {"contents":"package foo"}`)

	mu.Lock()
	defer mu.Unlock()
	if len(seq) != 2 {
		t.Fatalf("got %d hook calls, want 2", len(seq))
	}
	if seq[0]["__path"] != "/pre" {
		t.Errorf("seq[0].path = %v, want /pre", seq[0]["__path"])
	}
	if seq[1]["__path"] != "/post" {
		t.Errorf("seq[1].path = %v, want /post", seq[1]["__path"])
	}
	if seq[0]["tool_use_id"] != seq[1]["tool_use_id"] {
		t.Errorf("tool_use_id mismatch: pre=%v post=%v", seq[0]["tool_use_id"], seq[1]["tool_use_id"])
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

// decisionRouter is a single httptest.Server whose handler returns a
// canned JSON body based on the request path. Hits are captured in the
// supplied slice for assertions on event order + payload shape.
func decisionRouter(t *testing.T, hits *[]map[string]any, mu *sync.Mutex, bodyByPath map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["__path"] = r.URL.Path
		mu.Lock()
		*hits = append(*hits, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		if resp, ok := bodyByPath[r.URL.Path]; ok {
			_, _ = io.WriteString(w, resp)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSlash_FakeTool_BlockedByHook_FiresPostWithError(t *testing.T) {
	t.Parallel()
	var (
		mu   sync.Mutex
		hits []map[string]any
	)
	srv := decisionRouter(t, &hits, &mu, map[string]string{
		"/pre": `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"path not allowed"}}`,
	})

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PreToolUse":  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool Bash {"command":"rm -rf /"}`)

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 {
		t.Fatalf("got %d hooks, want 2 (Pre then Post)", len(hits))
	}
	if hits[0]["__path"] != "/pre" || hits[1]["__path"] != "/post" {
		t.Errorf("hook order = %v, %v; want /pre then /post", hits[0]["__path"], hits[1]["__path"])
	}
	post := hits[1]
	resp, _ := post["tool_response"].(map[string]any)
	if resp == nil || resp["error"] != "blocked" || resp["reason"] != "path not allowed" {
		t.Errorf("PostToolUse tool_response = %v, want {error:blocked, reason:path not allowed}", post["tool_response"])
	}
	if !strings.Contains(out.String(), "blocked by hook: path not allowed") {
		t.Errorf("output missing block marker; got %q", out.String())
	}
}

func TestSlash_FakeTool_BlockedWithPriorPending_FlushesPriorThenBlocks(t *testing.T) {
	t.Parallel()
	// Exercises the flush-prior code path: /fake-tool A (allowed) lands
	// in pending; /fake-tool B (denied) flushes A then short-circuits B.
	// One PreToolUse matcher that branches on tool_name so the
	// aggregation rule (any-block-wins) doesn't bleed B's deny into A.
	var (
		mu   sync.Mutex
		hits []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["__path"] = r.URL.Path
		mu.Lock()
		hits = append(hits, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		if r.URL.Path == "/pre" && body["tool_name"] == "B" {
			_, _ = io.WriteString(w, `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"B denied"}}`)
		}
	}))
	t.Cleanup(srv.Close)

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PreToolUse":  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	ctx := context.Background()
	h.Dispatch(ctx, `/fake-tool A {}`)
	h.Dispatch(ctx, `/fake-tool B {}`)

	mu.Lock()
	defer mu.Unlock()
	// Pre-A, Post-A (flush), Pre-B, Post-B (blocked) = 4 hits.
	if len(hits) != 4 {
		t.Fatalf("got %d hooks, want 4:\n%v", len(hits), hits)
	}
	wantPaths := []string{"/pre", "/post", "/pre", "/post"}
	wantNames := []string{"A", "A", "B", "B"}
	for i, h := range hits {
		if h["__path"] != wantPaths[i] {
			t.Errorf("hits[%d].__path = %v, want %v", i, h["__path"], wantPaths[i])
		}
		if h["tool_name"] != wantNames[i] {
			t.Errorf("hits[%d].tool_name = %v, want %v", i, h["tool_name"], wantNames[i])
		}
	}
	// Post-A is the flush (nil response per the flush convention).
	if _, ok := hits[1]["tool_response"]; ok && hits[1]["tool_response"] != nil {
		t.Errorf("flush Post-A tool_response = %v, want nil", hits[1]["tool_response"])
	}
	// Post-B is the blocked synthetic.
	resp, _ := hits[3]["tool_response"].(map[string]any)
	if resp == nil || resp["error"] != "blocked" || resp["reason"] != "B denied" {
		t.Errorf("blocked Post-B tool_response = %v, want {error:blocked, reason:B denied}", hits[3]["tool_response"])
	}
}

func TestSlash_FakeTool_BlockedThenResult_NoExtraPost(t *testing.T) {
	t.Parallel()
	// After a block-and-Post lifecycle, a stray /fake-tool-result must
	// not fire another PostToolUse — no pending entry should remain.
	var (
		mu   sync.Mutex
		hits []map[string]any
	)
	srv := decisionRouter(t, &hits, &mu, map[string]string{
		"/pre": `{"hookSpecificOutput":{"permissionDecision":"deny"}}`,
	})

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PreToolUse":  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool Bash {}`)
	h.Dispatch(context.Background(), `/fake-tool-result {"contents":"x"}`)

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 {
		t.Fatalf("got %d hooks, want 2 (Pre + the blocked Post; the orphan result must not fire)", len(hits))
	}
}

func TestSlash_FakeTool_AwaitingPermission_ResultShortCircuits(t *testing.T) {
	t.Parallel()
	var (
		mu   sync.Mutex
		hits []map[string]any
	)
	srv := decisionRouter(t, &hits, &mu, map[string]string{
		"/pre": `{"hookSpecificOutput":{"permissionDecision":"ask","permissionDecisionReason":"please confirm"}}`,
	})

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PreToolUse":  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool Bash {}`)
	h.Dispatch(context.Background(), `/fake-tool-result {"contents":"x"}`)

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 {
		t.Errorf("got %d hooks, want 1 (Pre only; /fake-tool-result must short-circuit)", len(hits))
	}
	if !strings.Contains(out.String(), "awaiting permission: please confirm") {
		t.Errorf("output missing awaiting marker; got %q", out.String())
	}
	if !strings.Contains(out.String(), "still awaiting permission") {
		t.Errorf("output missing short-circuit marker on /fake-tool-result; got %q", out.String())
	}
}

func TestSlash_FakeTool_AwaitingPermission_DisplacedByNextTool(t *testing.T) {
	t.Parallel()
	// /fake-tool A enters awaitingPermission via /pre's ask response;
	// /fake-tool B then arrives and must flush A with the blocked
	// synthetic shape (not the legacy nil response).
	var (
		mu   sync.Mutex
		hits []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["__path"] = r.URL.Path
		mu.Lock()
		hits = append(hits, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		if r.URL.Path == "/pre" && body["tool_name"] == "A" {
			_, _ = io.WriteString(w, `{"hookSpecificOutput":{"permissionDecision":"ask","permissionDecisionReason":"confirm A"}}`)
		}
	}))
	t.Cleanup(srv.Close)

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PreToolUse":  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	ctx := context.Background()
	h.Dispatch(ctx, `/fake-tool A {}`)
	h.Dispatch(ctx, `/fake-tool B {}`)

	mu.Lock()
	defer mu.Unlock()
	// Pre-A (ask), Post-A (displaced flush, blocked synthetic), Pre-B (allow), B remains pending — 3 hits.
	if len(hits) != 3 {
		t.Fatalf("got %d hooks, want 3:\n%v", len(hits), hits)
	}
	if hits[1]["__path"] != "/post" || hits[1]["tool_name"] != "A" {
		t.Fatalf("hits[1] = %v, want Post for A", hits[1])
	}
	resp, _ := hits[1]["tool_response"].(map[string]any)
	if resp == nil || resp["error"] != "blocked" {
		t.Errorf("displaced Post-A tool_response = %v, want {error:blocked, reason:...}", hits[1]["tool_response"])
	}
	if reason, _ := resp["reason"].(string); !strings.Contains(reason, "replaced") {
		t.Errorf("displaced Post-A reason = %q, want substring %q", reason, "replaced")
	}
}

func TestSlash_FakeTool_AwaitingPermission_FlushOnShutdown(t *testing.T) {
	t.Parallel()
	var (
		mu   sync.Mutex
		hits []map[string]any
	)
	srv := decisionRouter(t, &hits, &mu, map[string]string{
		"/pre": `{"hookSpecificOutput":{"permissionDecision":"ask"}}`,
	})

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	h.hooks = hooks.NewSender(map[string][]hooks.Matcher{
		"PreToolUse":  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		"PostToolUse": {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)

	h.Dispatch(context.Background(), `/fake-tool Bash {}`)
	h.FlushPendingTool(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 {
		t.Fatalf("got %d hooks, want 2 (Pre then Post-on-flush)", len(hits))
	}
	post := hits[1]
	if post["__path"] != "/post" {
		t.Fatalf("second hook path = %v, want /post", post["__path"])
	}
	resp, _ := post["tool_response"].(map[string]any)
	if resp == nil || resp["error"] != "blocked" {
		t.Errorf("flush PostToolUse tool_response = %v, want {error:blocked, ...}", post["tool_response"])
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

	cases := []struct {
		name     string
		line     string
		wantCode int
	}{
		{name: "/exit no code", line: "/exit", wantCode: 0},
		{name: "/exit with code", line: "/exit 7", wantCode: 7},
		{name: "/quit no code (alias)", line: "/quit", wantCode: 0},
		{name: "/quit with code (alias)", line: "/quit 3", wantCode: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h := newTestHandler(out)
			oc := h.Dispatch(context.Background(), tc.line)
			if !oc.Exit || oc.ExitCode != tc.wantCode {
				t.Errorf("%s got Exit=%v Code=%d, want true/%d", tc.line, oc.Exit, oc.ExitCode, tc.wantCode)
			}
			if oc.Reason != "logout" {
				t.Errorf("Reason = %q, want logout", oc.Reason)
			}
		})
	}
}

// TestSlash_LifecycleCommands asserts /clear, /compact, and
// /fake-auto-compact dispatch to the right Outcome.
func TestSlash_LifecycleCommands(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		line        string
		wantReason  string
		wantTrigger string
	}{
		{name: "/clear", line: "/clear", wantReason: "clear", wantTrigger: ""},
		{name: "/compact", line: "/compact", wantReason: "compact", wantTrigger: "manual"},
		{name: "/fake-auto-compact", line: "/fake-auto-compact", wantReason: "compact", wantTrigger: "auto"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := &bytes.Buffer{}
			h := newTestHandler(out)
			oc := h.Dispatch(context.Background(), tc.line)
			if !oc.Handled || !oc.Restart {
				t.Fatalf("Handled=%v Restart=%v, want both true", oc.Handled, oc.Restart)
			}
			if oc.RestartReason != tc.wantReason {
				t.Errorf("RestartReason = %q, want %q", oc.RestartReason, tc.wantReason)
			}
			if oc.CompactTrigger != tc.wantTrigger {
				t.Errorf("CompactTrigger = %q, want %q", oc.CompactTrigger, tc.wantTrigger)
			}
			if oc.Exit {
				t.Errorf("Exit = true, want false")
			}
		})
	}
}

// TestSlash_RestartRemoved asserts /restart is no longer a recognized
// command — it falls through to the unknown-command path (Handled=true,
// no Restart outcome).
func TestSlash_RestartRemoved(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	h := newTestHandler(out)
	oc := h.Dispatch(context.Background(), "/restart compact")
	if !oc.Handled {
		t.Errorf("Handled = false, want true (unknown slash still consumed)")
	}
	if oc.Restart {
		t.Errorf("Restart = true, want false (/restart should no longer dispatch lifecycle)")
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
			name:      "stream usage when duration missing",
			line:      "/stream hello world",
			wantInOut: "usage: /stream",
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
