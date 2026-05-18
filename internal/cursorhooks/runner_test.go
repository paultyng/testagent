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

// TestNoOpLifecycleMethods proves the five HookSender methods Cursor
// has no equivalent event for (OnPrompt, OnSessionStart, OnSessionEnd,
// OnPreCompact, OnPostCompact) silently swallow registered matchers
// rather than firing them. A user might register, e.g., a "sessionStart"
// matcher in their hooks.json (cursor docs name it that way), but
// testagent's runner does NOT wire those names today and these methods
// return nil without running anything.
func TestNoOpLifecycleMethods(t *testing.T) {
	skipWindows(t)
	t.Parallel()

	cases := []struct {
		name     string
		event    string
		call     func(context.Context, *cursorhooks.Runner) error
	}{
		{
			name:  "OnPrompt",
			event: "userPromptSubmit", // a name a user might guess
			call: func(ctx context.Context, r *cursorhooks.Runner) error {
				return r.OnPrompt(ctx, "hi", "session title")
			},
		},
		{
			name:  "OnSessionStart",
			event: "sessionStart",
			call: func(ctx context.Context, r *cursorhooks.Runner) error {
				return r.OnSessionStart(ctx, "startup")
			},
		},
		{
			name:  "OnSessionEnd",
			event: "sessionEnd",
			call: func(ctx context.Context, r *cursorhooks.Runner) error {
				return r.OnSessionEnd(ctx, "logout")
			},
		},
		{
			name:  "OnPreCompact",
			event: "preCompact",
			call: func(ctx context.Context, r *cursorhooks.Runner) error {
				return r.OnPreCompact(ctx, "manual")
			},
		},
		{
			name:  "OnPostCompact",
			event: "postCompact",
			call: func(ctx context.Context, r *cursorhooks.Runner) error {
				return r.OnPostCompact(ctx, "manual")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			marker := filepath.Join(dir, "ran.txt")
			script := writeScript(t, dir, "noop.sh", fmt.Sprintf("touch %s", marker))
			r := newRunner(map[string][]cursorhooks.Matcher{
				tc.event: {{Command: script}},
			})
			if err := tc.call(context.Background(), r); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Errorf("matcher fired for %s (marker exists); should be no-op", tc.name)
			}
		})
	}
}

// TestOnPermissionRequestFiresPreToolUse asserts that OnPermissionRequest
// routes to the preToolUse event (cursor models permission decisions as
// the gate, not a separate event) and honors per-tool matcher filtering.
func TestOnPermissionRequestFiresPreToolUse(t *testing.T) {
	t.Parallel()
	skipWindows(t)

	dir := t.TempDir()
	deny := writeScript(t, dir, "deny.sh",
		`echo '{"permission":"deny","agent_message":"perm denied"}'`)
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventPreToolUse: {
			{Command: deny, Pattern: "Edit"},
		},
	})

	// Wrong tool name → no fire.
	resOther, err := r.OnPermissionRequest(context.Background(), "id1", "Shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resOther.Block {
		t.Errorf("expected no block for Shell (pattern=Edit), got %+v", resOther)
	}

	// Matching tool name → deny fires.
	resEdit, err := r.OnPermissionRequest(context.Background(), "id1", "Edit", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resEdit.Block {
		t.Errorf("expected Block=true for Edit, got %+v", resEdit)
	}
	if resEdit.Reason != "perm denied" {
		t.Errorf("expected Reason=%q, got %q", "perm denied", resEdit.Reason)
	}
}

// TestOnStopFiresStopEvent asserts OnStop dispatches a registered "stop"
// matcher. Stop is advisory in hookresult aggregation (returns the zero
// Result), but the matcher must still execute so users observing the
// transcript see the side effect.
func TestOnStopFiresStopEvent(t *testing.T) {
	t.Parallel()
	skipWindows(t)

	dir := t.TempDir()
	marker := filepath.Join(dir, "stopped.txt")
	script := writeScript(t, dir, "stop.sh",
		fmt.Sprintf("touch %s", marker))
	r := newRunner(map[string][]cursorhooks.Matcher{
		cursorhooks.EventStop: {{Command: script}},
	})
	if err := r.OnStop(context.Background(), "last message", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("expected stop hook to have run (marker file missing)")
	}
}

// TestRunnerCloseIsNoOp asserts Close returns nil without doing anything
// observable. Cursor has no async machinery so there's nothing to drain.
func TestRunnerCloseIsNoOp(t *testing.T) {
	t.Parallel()
	r := newRunner(nil)
	if err := r.Close(context.Background()); err != nil {
		t.Errorf("Close returned non-nil: %v", err)
	}
	// Idempotent.
	if err := r.Close(context.Background()); err != nil {
		t.Errorf("second Close returned non-nil: %v", err)
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
