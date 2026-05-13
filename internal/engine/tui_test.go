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
	"github.com/paultyng/testagent/internal/render"
	"github.com/paultyng/testagent/internal/slash"
)

// testOpts is the optional override bag for newTestModel.
type testOpts struct {
	Name       string
	ThinkDelay time.Duration
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

// firstMsgOfType drills into a tea.Cmd (which may be a tea.Batch) and
// returns the first message of type T it finds. Used by slash-dispatch
// tests since startTurn now returns a Batch (commit + slash dispatch).
func firstMsgOfType[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var zero T
	if cmd == nil {
		t.Fatalf("cmd is nil; can't extract %T", zero)
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, inner := range batch {
			if inner == nil {
				continue
			}
			if v, ok := inner().(T); ok {
				return v
			}
		}
		t.Fatalf("no %T in BatchMsg of %d cmds", zero, len(batch))
	}
	if v, ok := msg.(T); ok {
		return v
	}
	t.Fatalf("expected %T, got %T", zero, msg)
	return zero
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
	for _, line := range m.scrollback {
		if strings.Contains(line, "[Test]") && strings.Contains(line, "hi") {
			foundEcho = true
		}
		if strings.Contains(line, "Thought for ") {
			foundThought = true
		}
	}
	if !foundEcho {
		t.Errorf("history missing [Test] hi echo:\n%v", m.scrollback)
	}
	if !foundThought {
		t.Errorf("history missing 'Thought for' marker:\n%v", m.scrollback)
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
	done := firstMsgOfType[slashDoneMsg](t, cmd)
	newM, _ := m.Update(done)
	m = newM.(model)

	joined := strings.Join(m.scrollback, "\n")
	// The /panel renders a rounded-border box; rounded corners ╭/╰ or ─ should appear.
	if !strings.ContainsAny(joined, "╭─") {
		t.Errorf("history missing panel border:\n%s", joined)
	}
	if !strings.Contains(joined, "hi") {
		t.Errorf("history missing panel content:\n%s", joined)
	}
}

// TestModel_SlashLifecycleResetsScrollback covers /clear, /compact, and
// /fake-auto-compact across both header shapes (banner only vs banner +
// status line). Each case asserts that after the lifecycle dispatch,
// m.scrollback is reset (the visible terminal is wiped) and then
// re-seeded with banner, optional status line, the user-echo line, and a
// Compacted marker (compact flavors only).
func TestModel_SlashLifecycleResetsScrollback(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusLine string
		slashLine  string
		wantMarker bool
	}{
		{name: "clear-no-status", slashLine: "/clear"},
		{name: "clear-with-status", statusLine: "hooks: stop", slashLine: "/clear"},
		{name: "compact-no-status", slashLine: "/compact", wantMarker: true},
		{name: "compact-with-status", statusLine: "hooks: stop", slashLine: "/compact", wantMarker: true},
		{name: "fake-auto-compact-no-status", slashLine: "/fake-auto-compact", wantMarker: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newTestModel(nil)
			m.g.StatusLine = tc.statusLine
			// Seed prior conversation noise that the lifecycle should wipe.
			m.scrollback = append(m.scrollback, "> earlier prompt", "Thought for 10ms", "[Test] earlier prompt")

			m = typeInto(m, tc.slashLine)
			m, cmd := pressEnter(m)
			done := firstMsgOfType[slashDoneMsg](t, cmd)
			newM, _ := m.Update(done)
			m = newM.(model)

			joined := strings.Join(m.scrollback, "\n")
			if strings.Contains(joined, "earlier prompt") {
				t.Errorf("scrollback still contains pre-lifecycle content: %v", m.scrollback)
			}
			// Banner re-emit always lands first; it carries the session id.
			if !strings.Contains(m.scrollback[0], "sid-test") {
				t.Errorf("scrollback[0] = %q, want banner containing sid-test", m.scrollback[0])
			}
			expectedLen := 2 // banner + user echo
			if tc.statusLine != "" {
				expectedLen++
				if !strings.Contains(m.scrollback[1], tc.statusLine) {
					t.Errorf("scrollback[1] = %q, want status line containing %q", m.scrollback[1], tc.statusLine)
				}
			}
			if tc.wantMarker {
				expectedLen++
			}
			if len(m.scrollback) != expectedLen {
				t.Fatalf("scrollback len = %d, want %d: %v", len(m.scrollback), expectedLen, m.scrollback)
			}
			// User echo is second-to-last for compact flavors, last for clear.
			echoIdx := len(m.scrollback) - 1
			if tc.wantMarker {
				echoIdx--
			}
			if !strings.Contains(m.scrollback[echoIdx], tc.slashLine) {
				t.Errorf("scrollback[%d] = %q, want user echo containing %q", echoIdx, m.scrollback[echoIdx], tc.slashLine)
			}
			if tc.wantMarker {
				if !strings.Contains(m.scrollback[len(m.scrollback)-1], "Compacted") {
					t.Errorf("last scrollback line = %q, want Compacted marker", m.scrollback[len(m.scrollback)-1])
				}
			} else if strings.Contains(joined, "Compacted") {
				t.Errorf("scrollback must not contain Compacted marker for %s: %v", tc.slashLine, m.scrollback)
			}
		})
	}
}

// TestModel_ViewBottomPaneOnly asserts View renders only the live bottom
// block (spinner / streaming line / queue / input). Committed scrollback
// content lives in the terminal's native buffer above the program and
// must NOT appear in the View frame.
func TestModel_ViewBottomPaneOnly(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	m.width = 80
	m.height = 10
	m.scrollback = append(m.scrollback, "BANNER", "> hi", "[Test] hi", "Thought for 1s")

	frame := m.View().Content
	for _, committed := range []string{"BANNER", "> hi", "[Test] hi", "Thought for 1s"} {
		if strings.Contains(frame, committed) {
			t.Errorf("View frame must not contain committed content %q (committed lines live in scrollback): %q", committed, frame)
		}
	}
}

// TestModel_ShiftEnterInsertsNewline asserts shift+enter inserts a newline
// into the multi-line textarea without submitting, and plain Enter then
// submits the full multi-line value.
func TestModel_ShiftEnterInsertsNewline(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	m = typeInto(m, "line one")
	// Shift+Enter: textarea inserts a newline; model does NOT submit.
	newM, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	m = newM.(model)
	if m.thinking {
		t.Fatal("shift+enter must not start a turn")
	}
	if !strings.Contains(m.input.Value(), "\n") {
		t.Errorf("input value = %q, want embedded newline after shift+enter", m.input.Value())
	}
	m = typeInto(m, "line two")
	// Plain Enter submits the multi-line value.
	m, _ = pressEnter(m)
	if !m.thinking {
		t.Fatal("plain Enter should start the turn")
	}
	if m.thinkingInput != "line one\nline two" {
		t.Errorf("thinkingInput = %q, want %q", m.thinkingInput, "line one\nline two")
	}
}

func TestModel_SlashExitQuits(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	m = typeInto(m, "/exit 7")
	m, cmd := pressEnter(m)
	done := firstMsgOfType[slashDoneMsg](t, cmd)
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
	if m.width != 40 || m.height != 10 {
		t.Errorf("model size = %dx%d, want 40x10", m.width, m.height)
	}
	if m.input.Width() == 0 {
		t.Errorf("input.Width = 0, want > 0 after WindowSizeMsg")
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
	for _, line := range m.scrollback {
		if strings.Contains(line, "Interrupted") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("history missing Interrupted marker:\n%v", m.scrollback)
	}

	// A stale thinkingDoneMsg with the old tag must be ignored.
	stale := thinkingDoneMsg{tag: originalTag, name: "Test", body: "hello"}
	newM, _ = m.Update(stale)
	m = newM.(model)
	for _, line := range m.scrollback {
		if strings.Contains(line, "[Test] hello") {
			t.Errorf("stale thinkingDoneMsg leaked into history: %q", line)
		}
	}
}

// TestCmdThink_ZeroDelaySynchronous pins the synchronous fast-path for
// delay=0: cmdThink must dispatch thinkingDoneMsg directly rather than
// going through tea.Tick's timer goroutine (which would change ordering
// semantics in tests and add cosmetic latency in callers that want "no
// spinner"). Regression guard — the path was silently dropped during
// the inline-rendering rewrite and restored after review.
func TestCmdThink_ZeroDelaySynchronous(t *testing.T) {
	t.Parallel()

	cmd := cmdThink(0, 0, 42, "Test", "hello world")
	if cmd == nil {
		t.Fatal("cmdThink returned nil cmd")
	}
	msg := cmd()
	done, ok := msg.(thinkingDoneMsg)
	if !ok {
		t.Fatalf("expected thinkingDoneMsg, got %T", msg)
	}
	if done.tag != 42 || done.name != "Test" || done.body != "hello world" {
		t.Errorf("done = %+v, want {tag:42 name:Test body:hello world}", done)
	}
}

// TestModel_ViewShowsBottomPaneContent asserts the View positively
// renders the live bottom-pane composition: spinner row when thinking,
// streaming line when streaming, queued: prefix per pending entry, and
// the input row at the bottom. Complement to TestModel_ViewBottomPaneOnly
// which only asserts the negative.
func TestModel_ViewShowsBottomPaneContent(t *testing.T) {
	t.Parallel()

	t.Run("thinking-spinner-and-queue", func(t *testing.T) {
		t.Parallel()
		m := newTestModel(nil)
		m.thinking = true
		m.thinkStart = time.Now()
		m.pending = []string{"queued one", "queued two"}
		frame := m.View().Content
		if !strings.Contains(frame, "thinking…") {
			t.Errorf("frame missing thinking row: %q", frame)
		}
		if !strings.Contains(frame, "queued one") || !strings.Contains(frame, "queued two") {
			t.Errorf("frame missing queue entries: %q", frame)
		}
		// Input row must be the last non-empty line.
		lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
		// The trailing input row from textarea is non-empty (carries the
		// prompt prefix) but may render as multiple lines due to dynamic
		// height; we just verify it sits below the queue lines.
		joined := strings.Join(lines, "\n")
		queuedIdx := strings.Index(joined, "queued two")
		inputIdx := strings.LastIndex(joined, render.Prompt())
		if inputIdx < 0 {
			t.Errorf("frame missing input prompt: %q", joined)
		}
		if inputIdx <= queuedIdx {
			t.Errorf("input prompt should render below queue; queue at %d, input at %d", queuedIdx, inputIdx)
		}
	})

	t.Run("streaming-line", func(t *testing.T) {
		t.Parallel()
		m := newTestModel(nil)
		m.streaming = true
		m.streamLine = "[Test] partial response so far"
		frame := m.View().Content
		if !strings.Contains(frame, "[Test] partial response so far") {
			t.Errorf("frame missing streaming line: %q", frame)
		}
	})
}

// TestModel_EscDuringStreamingCommitsPartial asserts Esc mid-stream
// commits the partial streamLine + an Interrupted marker to scrollback
// and clears streaming state. Covers the second branch of the cancelMsg
// handler that TestModel_EscCancelsThinking doesn't exercise.
// Stop-hook firing (cmdHookStop with stop_hook_active=true) happens via
// a returned tea.Cmd; this test doesn't inspect the cmd, so the hook
// payload itself isn't asserted here.
func TestModel_EscDuringStreamingCommitsPartial(t *testing.T) {
	t.Parallel()

	m := newTestModel(nil)
	m = typeInto(m, "hello world here")
	m, _ = pressEnter(m)
	// Transition to streaming.
	newM, _ := m.Update(thinkingDoneMsg{tag: m.turnTag, name: "Test", body: "hello world here", streamDelay: 0})
	m = newM.(model)
	if !m.streaming {
		t.Fatal("expected streaming=true after thinkingDoneMsg")
	}
	// Advance two of the four tokens so streamLine is partial.
	newM, _ = m.Update(streamChunkMsg{tag: m.turnTag})
	m = newM.(model)
	newM, _ = m.Update(streamChunkMsg{tag: m.turnTag})
	m = newM.(model)
	if m.streamIdx != 2 {
		t.Fatalf("streamIdx = %d, want 2 before Esc", m.streamIdx)
	}
	partialLine := m.streamLine // capture before cancel resets

	// Esc → cancelMsg → commit partial + Interrupted.
	newM, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = newM.(model)
	if cmd == nil {
		t.Fatal("expected cancelMsg cmd from Esc")
	}
	newM, _ = m.Update(cmd())
	m = newM.(model)

	if m.streaming {
		t.Errorf("expected streaming=false after Esc")
	}
	if m.streamLine != "" {
		t.Errorf("expected streamLine cleared, got %q", m.streamLine)
	}
	if m.streamTokens != nil {
		t.Errorf("expected streamTokens nil, got %v", m.streamTokens)
	}

	joined := strings.Join(m.scrollback, "\n")
	if !strings.Contains(joined, partialLine) {
		t.Errorf("scrollback missing partial stream line %q:\n%s", partialLine, joined)
	}
	if !strings.Contains(joined, "Interrupted") {
		t.Errorf("scrollback missing Interrupted marker:\n%s", joined)
	}
	// Partial line should come before the Interrupted marker.
	partialIdx := strings.Index(joined, partialLine)
	interruptIdx := strings.Index(joined, "Interrupted")
	if partialIdx >= interruptIdx {
		t.Errorf("expected partial line before Interrupted; partial at %d, interrupt at %d", partialIdx, interruptIdx)
	}
}

// TestCmdSlashRestart_FiresHooksInOrder pins the wire-order contract for
// the /clear and /compact lifecycle in the TUI path: a pending /fake-tool's
// PostToolUse must precede SessionEnd, and SessionEnd must precede
// SessionStart. Earlier wiring used tea.Batch which dispatched the two
// hook cmds concurrently and could race.
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
