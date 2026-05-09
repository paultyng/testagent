package main

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

	tea "github.com/charmbracelet/bubbletea"
)

// newTestModel builds a model wired up against in-memory hook/MCP/slash
// dependencies. opt can be nil for defaults.
func newTestModel(opt *tuiOptions) model {
	o := tuiOptions{
		name:       "Test",
		sessionID:  "sid-test",
		cwd:        "/tmp",
		delay:      10 * time.Millisecond,
		historyCap: 1000,
		hooks:      NewHookSender(nil, "sid-test", "/tmp", "", "default", nil),
		mcp:        NewMCPClient(nil),
	}
	if opt != nil {
		// Caller-provided overrides
		if opt.name != "" {
			o.name = opt.name
		}
		if opt.delay != 0 {
			o.delay = opt.delay
		}
		if opt.historyCap != 0 {
			o.historyCap = opt.historyCap
		}
		if opt.hooks != nil {
			o.hooks = opt.hooks
		}
		if opt.mcp != nil {
			o.mcp = opt.mcp
		}
	}
	o.slash = &SlashHandler{
		name:        o.name,
		streamDelay: 0,
		sessionID:   o.sessionID,
		cwd:         o.cwd,
		hooks:       o.hooks,
		mcp:         o.mcp,
	}
	return newModel(o)
}

// type-and-update feeds each rune through Update so the textinput sees them
// the same way bubbletea would dispatch them in the running program.
func typeInto(m model, s string) model {
	for _, r := range s {
		newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = newM.(model)
	}
	return m
}

// pressEnter dispatches an Enter key and returns the resulting model + cmd.
func pressEnter(m model) (model, tea.Cmd) {
	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return newM.(model), cmd
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

	// Drain commands until we see thinkingDoneMsg, then feed it back.
	// tea.Tick is asynchronous in the runtime; here we synthesize the
	// thinkingDoneMsg directly to advance past the delay.
	doneMsg := thinkingDoneMsg{tag: m.thinkTag, response: "[Test] hi"}
	newM, _ := m.Update(doneMsg)
	m = newM.(model)

	if m.thinking {
		t.Errorf("expected thinking=false after thinkingDoneMsg")
	}
	foundEcho := false
	foundThought := false
	for _, line := range m.history {
		if strings.Contains(line, "[Test] hi") {
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

	// Complete the first turn — the next prompt should re-enter thinking.
	doneMsg := thinkingDoneMsg{tag: m.thinkTag, response: "[Test] a"}
	newM, _ := m.Update(doneMsg)
	m = newM.(model)
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
	if m.input.Width != 38 {
		t.Errorf("input.Width = %d, want 38", m.input.Width)
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
	originalTag := m.thinkTag

	// Esc should send cancelMsg.
	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
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
	if m.thinkTag == originalTag {
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
	stale := thinkingDoneMsg{tag: originalTag, response: "[Test] hello"}
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

	o := tuiOptions{historyCap: 3}
	m := newTestModel(&o)
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

	settings := &Settings{
		Hooks: map[string][]HookMatcher{
			hookEventPostToolUse:  {{Hooks: []Hook{{Type: "http", URL: srv.URL + "/tool-use", Timeout: 1}}}},
			hookEventSessionStart: {{Hooks: []Hook{{Type: "http", URL: srv.URL + "/start", Timeout: 1}}}},
			hookEventSessionEnd:   {{Hooks: []Hook{{Type: "http", URL: srv.URL + "/end", Timeout: 1}}}},
		},
	}
	hooks := NewHookSender(settings, "sid-test", "/tmp", "", "default", nil)
	slash := &SlashHandler{
		name:      "Test",
		sessionID: "sid-test",
		cwd:       "/tmp",
		hooks:     hooks,
		mcp:       NewMCPClient(nil),
		out:       io.Discard,
	}

	// Stage a pending /fake-tool so we can prove its PostToolUse drains
	// before SessionEnd.
	slash.Dispatch(context.Background(), `/fake-tool read_file {"path":"foo.go"}`)

	cmd := cmdSlashRestart(slash, hooks, "compact")
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

func TestModel_CtrlCQuits(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = newM.(model)
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd from Ctrl+C")
	}
	if m.quitReason != "other" {
		t.Errorf("quitReason = %q, want %q", m.quitReason, "other")
	}
}
