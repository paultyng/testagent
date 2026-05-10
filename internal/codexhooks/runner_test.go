package codexhooks

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeTwoLineCmd returns a shell command string that writes the values
// of two env vars (one per line) to outPath. Portable across
// `$SHELL -lc` (Unix) and `cmd.exe /C` (Windows).
//
// Note: the Windows form deliberately avoids parens AND outer quotes
// around outPath. Go's exec wraps any cmd.exe argument containing
// spaces/special chars in `"..."` with backslash-escaped inner quotes,
// which cmd.exe /C does NOT understand (it doesn't recognize `\"` as a
// quote-escape). Using two `>` / `>>` redirects joined by `&` keeps the
// command free of inner quotes, and t.TempDir paths on the standard
// GitHub Windows runner are space-free (`C:\Users\RUNNER~1\...`).
func writeTwoLineCmd(envA, envB, outPath string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`echo %%%s%% > %s & echo %%%s%% >> %s`, envA, outPath, envB, outPath)
	}
	return fmt.Sprintf(`printf '%%s\n%%s\n' "${%s}" "${%s}" > %q`, envA, envB, outPath)
}

// sleepCmd returns a shell command string that sleeps for at least
// `seconds` seconds. Portable across Unix sh and Windows cmd: on
// Windows we use `ping -n N+1 127.0.0.1` (each ping waits ~1s after
// the previous, so N+1 pings ≈ N seconds wall time).
func sleepCmd(seconds int) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("ping -n %d 127.0.0.1 >NUL", seconds+1)
	}
	return fmt.Sprintf("sleep %d", seconds)
}

// TestRunner_FiresShellCommands writes a sentinel file from a hook's
// shell command and asserts the runner actually executed it. Real
// process via os/exec — that path is not stubbable, and asserting the
// real subprocess effect is the only honest test of what the runner
// will do in production.
func TestRunner_FiresShellCommands(t *testing.T) {
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
					Command: writeTwoLineCmd(tc.extraEnv, "CODEX_HOOK_SESSION_ID", out),
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
			// Windows cmd `echo` appends CRLF and may include trailing
			// spaces before the redirection; trim per-line.
			raw := strings.TrimRight(string(b), "\r\n")
			rawLines := strings.Split(raw, "\n")
			lines := make([]string, 0, len(rawLines))
			for _, ln := range rawLines {
				lines = append(lines, strings.TrimRight(ln, " \r"))
			}
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

	r := NewRunner(nil, "sid", t.TempDir(), "", "default", nil)
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
	// Portable "create file" command: `echo x > "path"` works in both
	// `sh -lc` and `cmd /C`.
	matchers := map[string][]Matcher{
		"session_end": {{Command: fmt.Sprintf(`echo x > %q`, out), Timeout: 5}},
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
	t.Parallel()

	// 1-second timeout; sleep ~5s → must abort within ~1s.
	matchers := map[string][]Matcher{
		EventStop: {{Command: sleepCmd(5), Timeout: 1}},
	}
	r := NewRunner(matchers, "sid", t.TempDir(), "", "default", nil)
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
	t.Parallel()

	var dbg bytes.Buffer
	matchers := map[string][]Matcher{
		EventStop: {{Command: "exit 0", Timeout: 5}},
	}
	r := NewRunner(matchers, "sid", t.TempDir(), "", "default", &dbg)
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
