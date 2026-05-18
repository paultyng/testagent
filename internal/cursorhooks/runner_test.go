package cursorhooks_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/paultyng/testagent/internal/cursorhooks"
)

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash scripts not supported on Windows")
	}
}

// writeScript writes a shell script to dir and returns its path.
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("writeScript: %v", err)
	}
	return p
}

func newRunner(matchers map[string][]cursorhooks.Matcher) *cursorhooks.Runner {
	return cursorhooks.NewRunner(matchers, "sess-1", "/tmp", "", "default", nil)
}

func TestNoMatchersForEvent(t *testing.T) {
	t.Parallel()
	r := newRunner(nil)
	res, err := r.OnPreToolUse(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Block || res.Allow || res.Ask {
		t.Errorf("expected zero result, got %+v", res)
	}
}

func TestSingleMatcherAllow(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "allow.sh",
		`echo '{"permission":"allow"}'`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: script},
		},
	})
	res, err := r.OnPreToolUse(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allow {
		t.Errorf("expected Allow=true, got %+v", res)
	}
}

func TestSingleMatcherDenyWithAgentMessage(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "deny.sh",
		`echo '{"permission":"deny","agent_message":"not allowed"}'`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: script},
		},
	})
	res, err := r.OnPreToolUse(context.Background(), "id1", "Read", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Block {
		t.Errorf("expected Block=true, got %+v", res)
	}
	if res.Reason != "not allowed" {
		t.Errorf("expected Reason=%q, got %q", "not allowed", res.Reason)
	}
}

func TestExit2BlocksWithStderr(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "exit2.sh",
		`echo "blocked" >&2; exit 2`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: script},
		},
	})
	res, err := r.OnPreToolUse(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Block {
		t.Errorf("expected Block=true, got %+v", res)
	}
	if res.Reason != "blocked" {
		t.Errorf("expected Reason=%q, got %q", "blocked", res.Reason)
	}
}

func TestPromptTypeMatcherSkipped(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	// If this script ran, it would return a deny.
	script := writeScript(t, dir, "prompt.sh",
		`echo '{"permission":"deny","agent_message":"should not run"}'`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: script, Type: "prompt"},
		},
	})
	res, err := r.OnPreToolUse(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Block || res.Allow || res.Ask {
		t.Errorf("expected zero result (prompt skipped), got %+v", res)
	}
}

func TestMultipleMatchersAnyDenyWins(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	allow := writeScript(t, dir, "allow.sh", `echo '{"permission":"allow"}'`)
	deny := writeScript(t, dir, "deny.sh",
		`echo '{"permission":"deny","agent_message":"denied by second"}'`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: allow},
			{Command: deny},
		},
	})
	res, err := r.OnPreToolUse(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Block {
		t.Errorf("expected Block=true, got %+v", res)
	}
}

func TestPatternFilteringByToolName(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	// This matcher is scoped to "Shell" only.
	deny := writeScript(t, dir, "deny.sh",
		`echo '{"permission":"deny","agent_message":"shell denied"}'`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: deny, Pattern: "Shell"},
		},
	})

	// "Read" should NOT trigger the Shell-scoped matcher.
	resRead, err := r.OnPreToolUse(context.Background(), "id1", "Read", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resRead.Block {
		t.Errorf("expected no block for Read (pattern=Shell), got %+v", resRead)
	}

	// "Shell" SHOULD trigger the deny.
	resShell, err := r.OnPreToolUse(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resShell.Block {
		t.Errorf("expected Block for Shell, got %+v", resShell)
	}
}

func TestFailClosedNonZeroNon2Exit(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	script := writeScript(t, dir, "fail.sh", `exit 1`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: script, FailClosed: true},
		},
	})
	res, err := r.OnPreToolUse(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Block {
		t.Errorf("expected Block=true (failClosed), got %+v", res)
	}
	if res.Reason != "hook failed (failClosed)" {
		t.Errorf("unexpected reason: %q", res.Reason)
	}
}

func TestOnPreToolUseCombinesPreToolUseAndBeforeShellExecution(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	// preToolUse returns allow; beforeShellExecution returns deny.
	allowScript := writeScript(t, dir, "allow.sh", `echo '{"permission":"allow"}'`)
	denyScript := writeScript(t, dir, "deny.sh",
		`echo '{"permission":"deny","agent_message":"shell gate denied"}'`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: allowScript},
		},
		cursorhooks.EventBeforeShellExecution: {
			{Command: denyScript},
		},
	})
	res, err := r.OnPreToolUse(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Block {
		t.Errorf("expected Block=true (beforeShellExecution deny wins), got %+v", res)
	}
	if res.Reason != "shell gate denied" {
		t.Errorf("unexpected reason: %q", res.Reason)
	}
}

func TestFailClosedNonZeroNon2ExitNonBlocking(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	// Without failClosed, non-zero non-2 exit is non-blocking (zero result).
	script := writeScript(t, dir, "fail.sh", `exit 1`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: script, FailClosed: false},
		},
	})
	res, err := r.OnPreToolUse(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Block {
		t.Errorf("expected no block (failClosed=false), got %+v", res)
	}
}

func TestOnPostToolUseShell(t *testing.T) {
	t.Parallel()
	skipWindows(t)
	dir := t.TempDir()
	// Advisory event — write to a temp file to prove the script ran.
	marker := filepath.Join(dir, "ran.txt")
	script := writeScript(t, dir, "after.sh",
		fmt.Sprintf(`touch %s`, marker))
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventAfterShellExecution: {
			{Command: script},
		},
	})
	err := r.OnPostToolUse(context.Background(), "id1", "Shell", nil, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(marker); os.IsNotExist(statErr) {
		t.Error("expected after-shell script to have run (marker file missing)")
	}
}
