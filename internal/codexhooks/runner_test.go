package codexhooks

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// skipIfNoPosixShell skips a test when the runtime has no `/bin/sh`.
// TODO(#45): once the runner routes through `cmd /c` (or fail-fasts)
// on Windows, replace these skips with equivalent assertions on the
// Windows-shell path. See
// https://github.com/paultyng/testagent/issues/45.
func skipIfNoPosixShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("codexhooks Runner hardcodes /bin/sh; Windows path is tracked in #45")
	}
}

// TestRunner_FiresShellCommands writes a sentinel file from a hook's
// shell command and asserts the runner actually executed it. Real
// process via os/exec — that path is not stubbable, and asserting the
// real subprocess effect is the only honest test of what the runner
// will do in production.
func TestRunner_FiresShellCommands(t *testing.T) {
	skipIfNoPosixShell(t)
	t.Parallel()

	tmp := t.TempDir()

	cases := []struct {
		name     string
		event    string
		fire     func(r *Runner) error
		extraEnv string // env var name we assert appears in the file
		extraVal string
	}{
		{
			name:     "session_start",
			event:    EventSessionStart,
			fire:     func(r *Runner) error { return r.OnSessionStart(context.Background(), "startup") },
			extraEnv: "CODEX_HOOK_SOURCE",
			extraVal: "startup",
		},
		{
			name:     "user_prompt_submit",
			event:    EventUserPromptSubmit,
			fire:     func(r *Runner) error { return r.OnPrompt(context.Background(), "hi", "demo") },
			extraEnv: "CODEX_HOOK_PROMPT",
			extraVal: "hi",
		},
		{
			name:     "stop",
			event:    EventStop,
			fire:     func(r *Runner) error { return r.OnStop(context.Background(), "[Codex] hi", false) },
			extraEnv: "CODEX_HOOK_LAST_ASSISTANT_MESSAGE",
			extraVal: "[Codex] hi",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := filepath.Join(tmp, tc.name+".out")
			matchers := map[string][]Matcher{
				tc.event: {{
					Command: `printf '%s\n%s\n' "${` + tc.extraEnv + `}" "${CODEX_HOOK_SESSION_ID}" > ` + out,
					Timeout: 5,
				}},
			}
			r := NewRunner(matchers, "sid-test", tmp, "/tmp/transcript.jsonl", "default", nil)
			if err := tc.fire(r); err != nil {
				t.Fatalf("fire %s: %v", tc.event, err)
			}
			b, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("hook did not write sentinel %s: %v", out, err)
			}
			lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(lines) != 2 {
				t.Fatalf("got %d sentinel lines, want 2: %q", len(lines), string(b))
			}
			if lines[0] != tc.extraVal {
				t.Errorf("env %s = %q, want %q", tc.extraEnv, lines[0], tc.extraVal)
			}
			if lines[1] != "sid-test" {
				t.Errorf("CODEX_HOOK_SESSION_ID = %q, want sid-test", lines[1])
			}
		})
	}
}

func TestRunner_NilMatchers_NoOp(t *testing.T) {
	t.Parallel()

	r := NewRunner(nil, "sid", "/tmp", "", "default", nil)
	ctx := context.Background()
	for _, fn := range []func() error{
		func() error { return r.OnSessionStart(ctx, "startup") },
		func() error { return r.OnPrompt(ctx, "hi", "title") },
		func() error { return r.OnStop(ctx, "msg", false) },
		func() error { return r.OnSessionEnd(ctx, "logout") },
		func() error { return r.OnToolUse(ctx, "id", "Tool", nil, nil, 0) },
	} {
		if err := fn(); err != nil {
			t.Errorf("nil matchers should be no-op, got %v", err)
		}
	}
}

func TestRunner_OnSessionEndIsNoOp(t *testing.T) {
	t.Parallel()

	// Even when matchers exist for a fictional `session_end` event, the
	// runner must NOT fire it — codex has no such hook upstream.
	tmp := t.TempDir()
	out := filepath.Join(tmp, "should-not-exist.out")
	matchers := map[string][]Matcher{
		"session_end": {{Command: "touch " + out, Timeout: 5}},
	}
	r := NewRunner(matchers, "sid", tmp, "", "default", nil)
	if err := r.OnSessionEnd(context.Background(), "logout"); err != nil {
		t.Errorf("OnSessionEnd: %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("session_end hook fired but should be a no-op (codex has no such event)")
	}
}

func TestRunner_TimeoutHonored(t *testing.T) {
	skipIfNoPosixShell(t)
	t.Parallel()

	// 1-second timeout; sleep 5 → must abort within ~1s.
	matchers := map[string][]Matcher{
		EventStop: {{Command: "sleep 5", Timeout: 1}},
	}
	r := NewRunner(matchers, "sid", "/tmp", "", "default", nil)
	start := time.Now()
	err := r.OnStop(context.Background(), "msg", false)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 3*time.Second {
		t.Errorf("hook took %s — timeout not honored", elapsed)
	}
}

func TestRunner_DebugWriterEmitsLine(t *testing.T) {
	skipIfNoPosixShell(t)
	t.Parallel()

	var dbg bytes.Buffer
	matchers := map[string][]Matcher{
		EventStop: {{Command: "true", Timeout: 5}},
	}
	r := NewRunner(matchers, "sid", "/tmp", "", "default", &dbg)
	if err := r.OnStop(context.Background(), "msg", false); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
	out := dbg.String()
	if !strings.Contains(out, "hook stop CMD") {
		t.Errorf("debug line missing prefix: %q", out)
	}
	if !strings.Contains(out, " OK ") {
		t.Errorf("debug line missing OK status: %q", out)
	}
}
