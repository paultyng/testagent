package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
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

// TestModel_SlashLifecyclePreservesInputPrompt asserts /clear and
// /compact don't break the textarea prompt rendering. Real-world bug:
// the original combined-escape Printf left bubbletea's cursor tracking
// confused and the textarea View() rendered without the "> " prefix
// after /clear; /compact happened to mask it (the extra Compacted
// commit nudged the redraw). Pins both branches to render the prompt
// after the lifecycle dispatch.
func TestModel_SlashLifecyclePreservesInputPrompt(t *testing.T) {
	t.Parallel()

	for _, slashLine := range []string{"/clear", "/compact"} {
		t.Run(slashLine, func(t *testing.T) {
			t.Parallel()
			m := newTestModel(nil)
			m = typeInto(m, slashLine)
			m, cmd := pressEnter(m)
			done := firstMsgOfType[slashDoneMsg](t, cmd)
			newM, _ := m.Update(done)
			m = newM.(model)

			// Textarea must still have the prompt configured and focus
			// preserved so the next user keystroke lands.
			if !m.input.Focused() {
				t.Errorf("textarea lost focus after %s", slashLine)
			}
			frame := m.View().Content
			if !strings.Contains(frame, render.Prompt()) {
				t.Errorf("View frame missing input prompt %q after %s:\n%s", render.Prompt(), slashLine, frame)
			}
		})
	}
}

// TestModel_EscDuringStreamingCommitsPartial asserts Esc mid-stream
// commits the partial streamLine + an Interrupted marker to scrollback,
// clears streaming state, and fires Stop with stop_hook_active=true and
// the reconstructed partial body. Covers the second branch of the
// cancelMsg handler that TestModel_EscCancelsThinking doesn't exercise.
func TestModel_EscDuringStreamingCommitsPartial(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		stops []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		stops = append(stops, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hookSender := hooks.NewSender(map[string][]hooks.Matcher{
		hooks.Stop: {{Hooks: []hooks.Hook{{Type: "http", URL: srv.URL + "/stop", Timeout: 1}}}},
	}, "sid-test", "/tmp", "", "default", nil)
	m := newTestModel(&testOpts{Hooks: hookSender})
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

	// Esc → cancelMsg → commit partial + Interrupted + fire Stop.
	newM, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = newM.(model)
	if cmd == nil {
		t.Fatal("expected cancelMsg cmd from Esc")
	}
	newM, cancelCmd := m.Update(cmd())
	m = newM.(model)

	// Execute every cmd returned from the cancelMsg Update so cmdHookStop
	// actually POSTs to the spy server. The returned cmd is a tea.Batch
	// containing the commit Println cmds plus cmdHookStop; iterate to find
	// and invoke each.
	if cancelCmd != nil {
		drainCmds(cancelCmd)
	}

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

	// Stop hook payload assertions: cmdHookStop fires asynchronously via
	// HTTP POST. Allow a brief moment for the goroutine to land.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(stops)
		mu.Unlock()
		if got >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(stops) != 1 {
		t.Fatalf("expected 1 Stop hook POST, got %d", len(stops))
	}
	body := stops[0]
	if body["stop_hook_active"] != true {
		t.Errorf("Stop body stop_hook_active = %v, want true", body["stop_hook_active"])
	}
	// The partial body submitted to the hook is "[Name] <space-joined tokens>".
	// With 2/4 tokens of "hello world here" advanced, the first two tokens are
	// "hello" and "world".
	wantPartial := "[Test] hello world"
	if body["last_assistant_message"] != wantPartial {
		t.Errorf("Stop body last_assistant_message = %q, want %q", body["last_assistant_message"], wantPartial)
	}
}

// drainCmds recursively calls every leaf tea.Cmd inside cmd, flattening
// tea.BatchMsg returns. Used by tests that want to observe the side
// effects of hook-firing cmds (e.g. an HTTP POST landing on a spy server)
// without driving the full bubbletea runtime.
//
// tea.sequenceMsg (from tea.Sequence) is intentionally not handled; all
// cancel-path cmds use tea.Batch. Extend when a test needs to drain
// Sequence-wrapped cmds.
func drainCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	switch v := msg.(type) {
	case tea.BatchMsg:
		for _, inner := range v {
			drainCmds(inner)
		}
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

// collectMsgs runs cmd and flattens any BatchMsg or tea.Sequence message
// (both are []tea.Cmd underneath) into leaf messages. Cmds
// that don't return within 50ms (ticks, timers) are skipped.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
			var out []tea.Msg
			for i := range v.Len() {
				out = append(out, collectMsgs(v.Index(i).Interface().(tea.Cmd))...)
			}
			return out
		}
		if msg == nil {
			return nil
		}
		return []tea.Msg{msg}
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// printLines returns the fmt.Sprint of every bubbletea print-line message.
func printLines(msgs []tea.Msg) []string {
	var out []string
	for _, msg := range msgs {
		if strings.HasSuffix(fmt.Sprintf("%T", msg), "printLineMessage") {
			out = append(out, fmt.Sprint(msg))
		}
	}
	return out
}

// TestBootScrollback pins that boot scrollback is one Println emitted only
// once both mcpConnectMsg and bootFrameMsg have arrived, in either order:
// bubbletea v2.0.7 mis-positions Printlns issued before the first frame
// flush, so nothing may print before then and the banner, status line, and
// MCP result must land in a single write.
func TestBootScrollback(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusLine string
		msg        mcpConnectMsg
		want       []string
		wantNot    []string
	}{
		{
			name:       "connected",
			statusLine: "hooks: stop",
			msg:        mcpConnectMsg{tools: 3},
			want:       []string{"Test", "[hooks: stop]", "[mcp connected: 3 tools]"},
		},
		{
			name:       "connect-failed",
			statusLine: "hooks: stop",
			msg:        mcpConnectMsg{err: errors.New("boom")},
			want:       []string{"Test", "[hooks: stop]", "[mcp connect failed: boom]"},
		},
		{
			name:       "start-error",
			statusLine: "hooks: stop",
			msg:        mcpConnectMsg{tools: 2, startErr: errors.New("hook down")},
			want:       []string{"Test", "[hooks: stop]", "[mcp connected: 2 tools]", "hook OnSessionStart error: hook down"},
		},
		{
			name: "no-mcp-no-status",
			msg:  mcpConnectMsg{},
			want: []string{"Test"},
			wantNot: []string{
				"mcp connect", "[hooks",
			},
		},
	}
	orders := []struct {
		name          string
		first, second func(mcpConnectMsg) tea.Msg
	}{
		{
			name:   "boot-then-frame",
			first:  func(c mcpConnectMsg) tea.Msg { return c },
			second: func(mcpConnectMsg) tea.Msg { return bootFrameMsg{} },
		},
		{
			name:   "frame-then-boot",
			first:  func(mcpConnectMsg) tea.Msg { return bootFrameMsg{} },
			second: func(c mcpConnectMsg) tea.Msg { return c },
		},
	}
	for _, tc := range cases {
		for _, ord := range orders {
			t.Run(tc.name+"/"+ord.name, func(t *testing.T) {
				t.Parallel()
				m := newTestModel(nil)
				m.g.StatusLine = tc.statusLine

				newM, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				if got := printLines(collectMsgs(cmd)); len(got) != 0 {
					t.Fatalf("first Update printed %d lines before boot completed: %q", len(got), got)
				}

				newM, cmd = newM.(model).Update(ord.first(tc.msg))
				if got := printLines(collectMsgs(cmd)); len(got) != 0 {
					t.Fatalf("first boot signal printed %d lines, want 0: %q", len(got), got)
				}
				if sb := newM.(model).scrollback; len(sb) != 0 {
					t.Fatalf("scrollback populated before both boot signals: %q", sb)
				}

				newM, cmd = newM.(model).Update(ord.second(tc.msg))
				got := printLines(collectMsgs(cmd))
				if len(got) != 1 {
					t.Fatalf("second boot signal emitted %d print lines, want exactly 1: %q", len(got), got)
				}
				pos := 0
				for _, w := range tc.want {
					i := strings.Index(got[0][pos:], w)
					if i < 0 {
						t.Fatalf("want %q (in order) after offset %d in %q", w, pos, got[0])
					}
					pos += i + len(w)
				}
				for _, w := range tc.wantNot {
					if strings.Contains(got[0], w) {
						t.Errorf("unexpected %q in %q", w, got[0])
					}
				}
				sb := newM.(model).scrollback
				if "{"+strings.Join(sb, "\n")+"}" != got[0] {
					t.Errorf("scrollback %q does not match printed %q", sb, got[0])
				}

				// A duplicate frame signal must not re-print.
				_, cmd = newM.(model).Update(bootFrameMsg{})
				if extra := printLines(collectMsgs(cmd)); len(extra) != 0 {
					t.Errorf("repeat bootFrameMsg printed %q", extra)
				}
			})
		}
	}
}

// TestBootScrollback_RestartBeforeBoot pins that a /clear or /compact
// landing before mcpConnectMsg latches the banner: the late boot result
// prints only its MCP/startErr lines, never a second banner.
func TestBootScrollback_RestartBeforeBoot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		slashLine string
		msg       mcpConnectMsg
		wantLate  []string // lines in the late boot Println; nil = no Println
	}{
		{name: "clear-connected", slashLine: "/clear", msg: mcpConnectMsg{tools: 3}, wantLate: []string{"[mcp connected: 3 tools]"}},
		{name: "compact-failed", slashLine: "/compact", msg: mcpConnectMsg{err: errors.New("boom")}, wantLate: []string{"[mcp connect failed: boom]"}},
		{name: "clear-start-error", slashLine: "/clear", msg: mcpConnectMsg{startErr: errors.New("hook down")}, wantLate: []string{"hook OnSessionStart error: hook down"}},
		{name: "clear-no-mcp", slashLine: "/clear", msg: mcpConnectMsg{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newTestModel(nil)
			m.g.StatusLine = "hooks: stop"
			newM, _ := m.Update(bootFrameMsg{})
			m = newM.(model)

			m = typeInto(m, tc.slashLine)
			m, cmd := pressEnter(m)
			done := firstMsgOfType[slashDoneMsg](t, cmd)
			newM, cmd = m.Update(done)
			m = newM.(model)
			printed := printLines(collectMsgs(cmd))

			newM, cmd = m.Update(tc.msg)
			m = newM.(model)
			late := printLines(collectMsgs(cmd))
			printed = append(printed, late...)

			if tc.wantLate == nil {
				if len(late) != 0 {
					t.Errorf("late boot printed %q, want nothing", late)
				}
			} else {
				if len(late) != 1 {
					t.Fatalf("late boot printed %d lines, want 1: %q", len(late), late)
				}
				if want := strings.Join(tc.wantLate, "\n"); !strings.Contains(late[0], want) {
					t.Errorf("late boot = %q, want %q", late[0], want)
				}
			}
			for name, text := range map[string]string{
				"scrollback": strings.Join(m.scrollback, "\n"),
				"printed":    strings.Join(printed, "\n"),
			} {
				if n := strings.Count(text, "sid-test"); n != 1 {
					t.Errorf("%s has %d banners, want 1: %q", name, n, text)
				}
				if n := strings.Count(text, "[hooks: stop]"); n != 1 {
					t.Errorf("%s has %d status lines, want 1: %q", name, n, text)
				}
			}
		})
	}
}

// TestBootScrollback_AutoExitFlushesPending pins that --auto-exit firing
// before boot printing completes flushes the pending boot lines and the
// auto-exit marker in one Println ahead of tea.Quit.
func TestBootScrollback_AutoExitFlushesPending(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pre     []tea.Msg
		want    []string
		wantNot []string
	}{
		{
			name: "nothing-arrived",
			want: []string{"sid-test", "[hooks: stop]", "[auto-exit after 1s]"},
		},
		{
			name: "boot-arrived-no-frame",
			pre:  []tea.Msg{mcpConnectMsg{tools: 2}},
			want: []string{"sid-test", "[hooks: stop]", "[mcp connected: 2 tools]", "[auto-exit after 1s]"},
		},
		{
			name: "frame-arrived-no-boot",
			pre:  []tea.Msg{bootFrameMsg{}},
			want: []string{"sid-test", "[hooks: stop]", "[auto-exit after 1s]"},
		},
		{
			name:    "already-flushed",
			pre:     []tea.Msg{bootFrameMsg{}, mcpConnectMsg{tools: 2}},
			want:    []string{"[auto-exit after 1s]"},
			wantNot: []string{"sid-test", "mcp connected"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newTestModel(nil)
			m.g.StatusLine = "hooks: stop"
			m.g.AutoExit = time.Second
			for _, msg := range tc.pre {
				newM, _ := m.Update(msg)
				m = newM.(model)
			}

			newM, cmd := m.Update(autoExitMsg{})
			msgs := collectMsgs(cmd)
			got := printLines(msgs)
			if len(got) != 1 {
				t.Fatalf("auto-exit printed %d lines, want 1: %q", len(got), got)
			}
			pos := 0
			for _, w := range tc.want {
				i := strings.Index(got[0][pos:], w)
				if i < 0 {
					t.Fatalf("want %q (in order) after offset %d in %q", w, pos, got[0])
				}
				pos += i + len(w)
			}
			for _, w := range tc.wantNot {
				if strings.Contains(got[0], w) {
					t.Errorf("unexpected %q in %q", w, got[0])
				}
			}
			quit := false
			for _, msg := range msgs {
				if _, ok := msg.(tea.QuitMsg); ok {
					quit = true
				}
			}
			if !quit {
				t.Error("auto-exit did not return tea.Quit")
			}
			if newM.(model).quitReason != "other" {
				t.Errorf("quitReason = %q, want other", newM.(model).quitReason)
			}
		})
	}
}

// TestCmdBoot_ConnectTimeout pins that a hung MCP server cannot block the
// boot result past the connect deadline, and that a timeout surfaces as the
// [mcp connect failed: ...] line. Real network IO, so real (short) time.
func TestCmdBoot_ConnectTimeout(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		hang     bool
		wantErr  bool
		wantLine string
	}{
		{name: "hung-server", hang: true, wantErr: true, wantLine: "[mcp connect failed: timed out after 100ms"},
		{name: "no-servers", wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := mcp.NewClient(nil)
			if tc.hang {
				release := make(chan struct{})
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					select {
					case <-r.Context().Done():
					case <-release:
					}
				}))
				t.Cleanup(srv.Close)
				t.Cleanup(func() { close(release) })
				client = mcp.NewClient(map[string]mcp.Server{"hang": {URL: srv.URL}})
			}
			m := newTestModel(&testOpts{MCP: client})

			ch := make(chan tea.Msg, 1)
			go func() { ch <- cmdBoot(client, m.d.Hooks, "startup", 100*time.Millisecond)() }()
			var msg tea.Msg
			select {
			case msg = <-ch:
			case <-time.After(5 * time.Second):
				t.Fatal("cmdBoot not bounded by the connect timeout")
			}
			boot, ok := msg.(mcpConnectMsg)
			if !ok {
				t.Fatalf("cmdBoot returned %T, want mcpConnectMsg", msg)
			}
			if (boot.err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", boot.err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(boot.err, context.DeadlineExceeded) {
				t.Errorf("err = %v, want wrapping context.DeadlineExceeded", boot.err)
			}

			newM, _ := m.Update(bootFrameMsg{})
			_, cmd := newM.(model).Update(boot)
			got := printLines(collectMsgs(cmd))
			if len(got) != 1 {
				t.Fatalf("boot printed %d lines, want 1: %q", len(got), got)
			}
			if tc.wantLine != "" && !strings.Contains(got[0], tc.wantLine) {
				t.Errorf("boot Println = %q, want %q", got[0], tc.wantLine)
			}
			if !tc.wantErr && strings.Contains(got[0], "mcp connect failed") {
				t.Errorf("unexpected connect failure in %q", got[0])
			}
		})
	}
}

// TestInit_SchedulesBootFrameTick pins that Init schedules bootFrameMsg at
// bootFrameDelay, at least two ~60fps frame intervals after start.
func TestInit_SchedulesBootFrameTick(t *testing.T) {
	t.Parallel()
	if bootFrameDelay < 2*time.Second/60 {
		t.Fatalf("bootFrameDelay = %s, want >= two 60fps frames", bootFrameDelay)
	}
	synctest.Test(t, func(t *testing.T) {
		cmd := newTestModel(nil).Init()
		batch, ok := cmd().(tea.BatchMsg)
		if !ok {
			t.Fatalf("Init returned %T, want BatchMsg", cmd())
		}
		start := time.Now()
		ch := make(chan time.Duration, len(batch))
		for _, c := range batch {
			go func() {
				if _, ok := c().(bootFrameMsg); ok {
					ch <- time.Since(start)
				}
			}()
		}
		select {
		case d := <-ch:
			if d != bootFrameDelay {
				t.Errorf("bootFrameMsg after %s, want %s", d, bootFrameDelay)
			}
		case <-time.After(time.Second):
			t.Fatal("Init never produced bootFrameMsg")
		}
	})
}
