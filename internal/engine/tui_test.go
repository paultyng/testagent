package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/slash"
)

// testOpts is the optional override bag for newTestModel.
type testOpts struct {
	Name       string
	ThinkDelay time.Duration
	HistoryCap int
	Hooks      *hooks.Sender
	MCP        *mcp.Client
}

// newTestModel builds a model wired up against in-memory hook/MCP/slash
// dependencies. opt can be nil for defaults.
func newTestModel(opt *testOpts) model {
	g := Globals{
		Name:       "Test",
		SessionID:  "sid-test",
		ThinkDelay: 10 * time.Millisecond,
		HistoryCap: 1000,
	}
	d := Deps{
		Hooks: hooks.NewSender(nil, "sid-test", "/tmp", "", "default", nil),
		MCP:   mcp.NewClient(nil),
	}
	if opt != nil {
		if opt.Name != "" {
			g.Name = opt.Name
		}
		if opt.ThinkDelay != 0 {
			g.ThinkDelay = opt.ThinkDelay
		}
		if opt.HistoryCap != 0 {
			g.HistoryCap = opt.HistoryCap
		}
		if opt.Hooks != nil {
			d.Hooks = opt.Hooks
		}
		if opt.MCP != nil {
			d.MCP = opt.MCP
		}
	}
	d.Slash = slash.New(d.Hooks, d.MCP, io.Discard)
	return newModel(g, d)
}

// type-and-update feeds each rune through Update so the textinput sees them
// the same way bubbletea would dispatch them in the running program. v2 keys
// carry a Code (rune) + Text (string of the actual characters); for printable
// runes we set both so msg.String() falls through to the default branch and
// textinput.Update reads msg.Text via insertRunesFromUserInput.
func typeInto(m model, s string) model {
	for _, r := range s {
		newM, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = newM.(model)
	}
	return m
}

// pressEnter dispatches an Enter key and returns the resulting model + cmd.
func pressEnter(m model) (model, tea.Cmd) {
	newM, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return newM.(model), cmd
}

// drainStream advances the model through a turn's streaming phase by
// synthesizing successive streamChunkMsg / streamDoneMsg until streaming
// ends. Mirrors what the bubbletea runtime would do for tea.Tick output.
func drainStream(t *testing.T, m model) model {
	t.Helper()
	for i := 0; m.streaming && i < 200; i++ {
		var msg tea.Msg
		if m.streamIdx >= len(m.streamTokens) {
			msg = streamDoneMsg{tag: m.turnTag, body: m.streamFinal}
		} else {
			msg = streamChunkMsg{tag: m.turnTag}
		}
		newM, _ := m.Update(msg)
		m = newM.(model)
	}
	if m.streaming {
		t.Fatalf("drainStream: model still streaming after 200 ticks")
	}
	return m
}

func TestModel_EnterSubmitsAndEchoes(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	m = typeInto(m, "hi")
	m, cmd := pressEnter(m)

	if !m.thinking {
		t.Fatalf("expected thinking=true after submitting prompt")
	}
	if cmd == nil {
		t.Fatalf("expected non-nil cmd from Enter submit")
	}

	// Synthesize thinkingDoneMsg to advance past the spinner phase. The
	// handler transitions into streaming and schedules the first chunk.
	doneMsg := thinkingDoneMsg{tag: m.turnTag, name: "Test", body: "hi", streamDelay: 0}
	newM, _ := m.Update(doneMsg)
	m = newM.(model)

	if m.thinking {
		t.Errorf("expected thinking=false after thinkingDoneMsg")
	}
	if !m.streaming {
		t.Errorf("expected streaming=true after thinkingDoneMsg")
	}

	// Drive the streaming phase to completion (chunk msgs + final done).
	m = drainStream(t, m)

	foundEcho := false
	foundThought := false
	for _, line := range m.history {
		if strings.Contains(line, "[Test]") && strings.Contains(line, "hi") {
			foundEcho = true
		}
		if strings.Contains(line, "Thought for ") {
			foundThought = true
		}
	}
	if !foundEcho {
		t.Errorf("history missing [Test] hi echo:\n%v", m.history)
	}
	if !foundThought {
		t.Errorf("history missing 'Thought for' marker:\n%v", m.history)
	}
}

func TestModel_TypingDuringThinkingIsQueued(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	m = typeInto(m, "a")
	m, _ = pressEnter(m)
	if !m.thinking {
		t.Fatal("expected to be thinking after first submit")
	}

	// Submit "b" while still thinking.
	m = typeInto(m, "b")
	m, _ = pressEnter(m)

	if len(m.pending) != 1 || m.pending[0] != "b" {
		t.Errorf("pending = %v, want [b]", m.pending)
	}

	// Complete the first turn end-to-end: thinking → streaming → done.
	// Only after streamDoneMsg does the queue drain.
	doneMsg := thinkingDoneMsg{tag: m.turnTag, name: "Test", body: "a", streamDelay: 0}
	newM, _ := m.Update(doneMsg)
	m = newM.(model)
	m = drainStream(t, m)

	if !m.thinking {
		t.Errorf("expected thinking=true after first turn drained the queue")
	}
	if m.thinkingInput != "b" {
		t.Errorf("thinkingInput = %q, want %q", m.thinkingInput, "b")
	}
	if len(m.pending) != 0 {
		t.Errorf("pending should be empty after drain, got %v", m.pending)
	}
}

func TestModel_SlashDispatchAppendsRendered(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	m = typeInto(m, "/panel hi")
	m, cmd := pressEnter(m)
	if cmd == nil {
		t.Fatal("expected cmd from slash dispatch")
	}
	msg := cmd()
	done, ok := msg.(slashDoneMsg)
	if !ok {
		t.Fatalf("expected slashDoneMsg, got %T", msg)
	}
	newM, _ := m.Update(done)
	m = newM.(model)

	joined := strings.Join(m.history, "\n")
	// The /panel renders a rounded-border box; rounded corners ╭/╰ or ─ should appear.
	if !strings.ContainsAny(joined, "╭─") {
		t.Errorf("history missing panel border:\n%s", joined)
	}
	if !strings.Contains(joined, "hi") {
		t.Errorf("history missing panel content:\n%s", joined)
	}
}

func TestModel_SlashExitQuits(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	m = typeInto(m, "/exit 7")
	m, cmd := pressEnter(m)
	if cmd == nil {
		t.Fatal("expected cmd from /exit")
	}
	msg := cmd()
	done, ok := msg.(slashDoneMsg)
	if !ok {
		t.Fatalf("expected slashDoneMsg, got %T", msg)
	}
	if !done.outcome.Exit || done.outcome.ExitCode != 7 {
		t.Errorf("outcome = %+v, want Exit=true ExitCode=7", done.outcome)
	}

	newM, cmd2 := m.Update(done)
	m = newM.(model)
	if m.quitCode != 7 {
		t.Errorf("quitCode = %d, want 7", m.quitCode)
	}
	if cmd2 == nil {
		t.Errorf("expected tea.Quit cmd after /exit slashDoneMsg")
	}
}

func TestModel_WindowSizeUpdatesInputWidth(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	newM, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = newM.(model)
	if m.input.Width() != 38 {
		t.Errorf("input.Width = %d, want 38", m.input.Width())
	}
	if m.width != 40 || m.height != 10 {
		t.Errorf("model size = %dx%d, want 40x10", m.width, m.height)
	}
}

func TestModel_EscCancelsThinking(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	m = typeInto(m, "hello")
	m, _ = pressEnter(m)
	if !m.thinking {
		t.Fatal("expected thinking=true")
	}
	originalTag := m.turnTag

	// Esc should send cancelMsg.
	newM, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = newM.(model)
	if cmd == nil {
		t.Fatal("expected cancelMsg cmd from Esc")
	}
	cancelMessage := cmd()
	if _, ok := cancelMessage.(cancelMsg); !ok {
		t.Fatalf("expected cancelMsg, got %T", cancelMessage)
	}

	// Apply the cancelMsg.
	newM, _ = m.Update(cancelMessage)
	m = newM.(model)

	if m.thinking {
		t.Errorf("expected thinking=false after Esc")
	}
	if m.turnTag == originalTag {
		t.Errorf("thinkTag should have advanced past %d", originalTag)
	}
	found := false
	for _, line := range m.history {
		if strings.Contains(line, "Interrupted") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("history missing Interrupted marker:\n%v", m.history)
	}

	// A stale thinkingDoneMsg with the old tag must be ignored.
	stale := thinkingDoneMsg{tag: originalTag, name: "Test", body: "hello"}
	newM, _ = m.Update(stale)
	m = newM.(model)
	for _, line := range m.history {
		if strings.Contains(line, "[Test] hello") {
			t.Errorf("stale thinkingDoneMsg leaked into history: %q", line)
		}
	}
}

func TestModel_HistoryCapEvictsOldest(t *testing.T) {
	t.Parallel()

	m := newTestModel(&testOpts{HistoryCap: 3})
	for _, s := range []string{"one", "two", "three", "four", "five"} {
		m.appendHistoryCapped(s)
	}
	if len(m.history) != 3 {
		t.Fatalf("history len = %d, want 3", len(m.history))
	}
	want := []string{"three", "four", "five"}
	for i, w := range want {
		if m.history[i] != w {
			t.Errorf("history[%d] = %q, want %q", i, m.history[i], w)
		}
	}
}

// TestCmdSlashRestart_FiresHooksInOrder pins the wire-order contract for
// /restart in the TUI path: a pending /fake-tool's PostToolUse must precede
// SessionEnd, and SessionEnd must precede SessionStart. Earlier wiring used
// tea.Batch which dispatched the two hook cmds concurrently and could race.
func TestCmdSlashRestart_FiresHooksInOrder(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		paths []string
		ends  []map[string]any // bodies posted to /end
		start map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/end":
			ends = append(ends, body)
		case "/start":
			start = body
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hookSender := hooks.NewSender(map[string][]hooks.Matcher{
		hooks.PostToolUse:  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/tool-use", Timeout: 1}}}},
		hooks.SessionStart: {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/start", Timeout: 1}}}},
		hooks.SessionEnd:   {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/end", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)
	handler := slash.New(hookSender, mcp.NewClient(nil), io.Discard)

	// Stage a pending /fake-tool so we can prove its PostToolUse drains
	// before SessionEnd.
	handler.Dispatch(context.Background(), `/fake-tool read_file {"path":"foo.go"}`)

	cmd := cmdSlashRestart(handler, hookSender, "compact", "")
	if cmd == nil {
		t.Fatal("cmdSlashRestart returned nil cmd")
	}
	if msg := cmd(); msg != nil {
		t.Errorf("expected nil tea.Msg on success, got %T %v", msg, msg)
	}

	mu.Lock()
	defer mu.Unlock()
	wantOrder := []string{"/tool-use", "/end", "/start"}
	if len(paths) != len(wantOrder) {
		t.Fatalf("paths = %v, want %v", paths, wantOrder)
	}
	for i, want := range wantOrder {
		if paths[i] != want {
			t.Errorf("paths[%d] = %q, want %q (full sequence: %v)", i, paths[i], want, paths)
		}
	}
	if len(ends) != 1 || ends[0]["reason"] != "compact" {
		t.Errorf("SessionEnd bodies = %v, want one with reason=compact", ends)
	}
	if start == nil || start["source"] != "compact" {
		t.Errorf("SessionStart body = %v, want source=compact", start)
	}
}

// TestCmdSlashRestart_CompactLifecycle pins the PreCompact → SessionEnd
// → SessionStart → PostCompact ordering for /compact and /fake-auto-compact
// in the TUI path, and asserts the trigger field is plumbed through.
func TestCmdSlashRestart_CompactLifecycle(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		paths    []string
		preBody  map[string]any
		postBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/pre":
			preBody = body
		case "/post":
			postBody = body
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hookSender := hooks.NewSender(map[string][]hooks.Matcher{
		hooks.PreCompact:   {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		hooks.SessionEnd:   {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/end", Timeout: 1}}}},
		hooks.SessionStart: {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/start", Timeout: 1}}}},
		hooks.PostCompact:  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)
	handler := slash.New(hookSender, mcp.NewClient(nil), io.Discard)

	cmd := cmdSlashRestart(handler, hookSender, "compact", "auto")
	if msg := cmd(); msg != nil {
		t.Errorf("expected nil tea.Msg on success, got %T %v", msg, msg)
	}

	mu.Lock()
	defer mu.Unlock()
	wantOrder := []string{"/pre", "/end", "/start", "/post"}
	if len(paths) != len(wantOrder) {
		t.Fatalf("paths = %v, want %v", paths, wantOrder)
	}
	for i, want := range wantOrder {
		if paths[i] != want {
			t.Errorf("paths[%d] = %q, want %q (full sequence: %v)", i, paths[i], want, paths)
		}
	}
	if preBody["trigger"] != "auto" || postBody["trigger"] != "auto" {
		t.Errorf("trigger: pre=%v post=%v, want auto/auto", preBody["trigger"], postBody["trigger"])
	}
}

// TestCmdSlashRestart_ClearSkipsCompactEvents asserts /clear (no
// compactTrigger) does NOT emit PreCompact/PostCompact.
func TestCmdSlashRestart_ClearSkipsCompactEvents(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		paths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hookSender := hooks.NewSender(map[string][]hooks.Matcher{
		hooks.PreCompact:   {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/pre", Timeout: 1}}}},
		hooks.SessionEnd:   {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/end", Timeout: 1}}}},
		hooks.SessionStart: {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/start", Timeout: 1}}}},
		hooks.PostCompact:  {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/post", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)
	handler := slash.New(hookSender, mcp.NewClient(nil), io.Discard)

	cmd := cmdSlashRestart(handler, hookSender, "clear", "")
	cmd()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"/end", "/start"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v (no Pre/PostCompact on /clear)", paths, want)
	}
	for i, w := range want {
		if paths[i] != w {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], w)
		}
	}
}

func TestModel_CtrlCQuits(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	newM, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = newM.(model)
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd from Ctrl+C")
	}
	if m.quitReason != "other" {
		t.Errorf("quitReason = %q, want %q", m.quitReason, "other")
	}
}
