// Package codexhooks runs Codex-shaped hooks. Codex's hook handlers are
// shell command strings configured under [hooks] in ~/.codex/config.toml,
// not HTTP POSTs like Claude's. The Runner satisfies the same
// engine.HookSender / slash.ToolHookSender interfaces internal/hooks
// satisfies, so vendor selection is just choosing which struct to
// build at the cmd/codex layer.
//
// MVP wires three events from #13:
//
//   - session_start
//   - user_prompt_submit
//   - stop
//
// Codex's HooksToml has no session_end; OnSessionEnd is a no-op.
// pre_tool_use / post_tool_use / pre_compact / post_compact are
// deferred (#34, #12).
package codexhooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// Codex hook event names. TOML keys are snake_case to match the
// upstream config schema.
const (
	EventSessionStart     = "session_start"
	EventUserPromptSubmit = "user_prompt_submit"
	EventStop             = "stop"
)

// defaultTimeout caps a synchronous matcher's wall-clock when the TOML
// doesn't specify one. Mirrors the conservative default Claude's HTTP
// hooks ship with.
const defaultTimeout = 10 * time.Second

// Matcher is one entry under a [hooks.<event>] array in the codex TOML.
// Mirrors the codex source's HookMatcher shape.
type Matcher struct {
	Command       string
	Async         bool
	Timeout       int // seconds; 0 → defaultTimeout
	StatusMessage string
}

// Runner fires shell-command hooks for codex events.
type Runner struct {
	matchers       map[string][]Matcher
	sessionID      string
	cwd            string
	transcriptPath string
	permissionMode string

	// debugWriter, when non-nil, receives one line per command attempt:
	// "hook <event> CMD <command-summary> <status|ERR> <elapsed> [err=...]"
	// Set to os.Stderr by --verbose. Plain text — never ANSI-styled, per
	// AGENTS.md.
	debugWriter io.Writer
}

// NewRunner returns a runner wired to the given matcher map. matchers
// may be nil (no-op runner) or omit any of the codex events. sessionID,
// cwd, transcriptPath, and permissionMode are passed to every shell
// command via the CODEX_HOOK_* env vars below. debugWriter is optional;
// nil silences trace output.
func NewRunner(matchers map[string][]Matcher, sessionID, cwd, transcriptPath, permissionMode string, debugWriter io.Writer) *Runner {
	return &Runner{
		matchers:       matchers,
		sessionID:      sessionID,
		cwd:            cwd,
		transcriptPath: transcriptPath,
		permissionMode: permissionMode,
		debugWriter:    debugWriter,
	}
}

// OnPrompt fires user_prompt_submit hooks.
func (r *Runner) OnPrompt(ctx context.Context, prompt, sessionTitle string) error {
	return r.fire(ctx, EventUserPromptSubmit, map[string]string{
		"CODEX_HOOK_PROMPT":        prompt,
		"CODEX_HOOK_SESSION_TITLE": sessionTitle,
	})
}

// OnToolUse is a no-op for MVP. pre_tool_use / post_tool_use wiring
// is tracked in #34.
func (r *Runner) OnToolUse(ctx context.Context, toolUseID, toolName string, toolInput, toolResponse any, durationMs int64) error {
	return nil
}

// OnStop fires stop hooks.
func (r *Runner) OnStop(ctx context.Context, lastAssistantMessage string, stopHookActive bool) error {
	return r.fire(ctx, EventStop, map[string]string{
		"CODEX_HOOK_LAST_ASSISTANT_MESSAGE": lastAssistantMessage,
		"CODEX_HOOK_STOP_HOOK_ACTIVE":       boolToString(stopHookActive),
	})
}

// OnSessionStart fires session_start hooks.
func (r *Runner) OnSessionStart(ctx context.Context, source string) error {
	return r.fire(ctx, EventSessionStart, map[string]string{
		"CODEX_HOOK_SOURCE": source,
	})
}

// OnSessionEnd is a no-op — codex's HooksToml has no session_end event.
// Engine still calls this on shutdown for parity with the claude path;
// the runner just returns nil so behavior matches what real codex would
// do (no shell command fires).
func (r *Runner) OnSessionEnd(ctx context.Context, reason string) error {
	return nil
}

// fire runs every matcher registered for event. Synchronous matchers
// honor their timeout; async matchers fire-and-forget on a goroutine
// (errors are logged via debugWriter only). Per-matcher errors are
// aggregated via errors.Join — one bad matcher does not stop the rest.
func (r *Runner) fire(ctx context.Context, event string, extraEnv map[string]string) error {
	matchers, ok := r.matchers[event]
	if !ok || len(matchers) == 0 {
		return nil
	}
	baseEnv := r.envFor(event, extraEnv)
	var errs []error
	for _, m := range matchers {
		if m.Async {
			go func(m Matcher) { _ = r.runOne(context.Background(), event, m, baseEnv) }(m)
			continue
		}
		if err := r.runOne(ctx, event, m, baseEnv); err != nil {
			errs = append(errs, fmt.Errorf("%s hook: %w", event, err))
		}
	}
	return errors.Join(errs...)
}

// runOne spawns /bin/sh -c <command>, applies the per-matcher timeout,
// and emits a debug line if debugWriter is set.
func (r *Runner) runOne(ctx context.Context, event string, m Matcher, env []string) (err error) {
	start := time.Now()
	defer func() {
		if r.debugWriter != nil {
			r.writeDebug(event, m.Command, time.Since(start), err)
		}
	}()

	timeout := time.Duration(m.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", m.Command)
	cmd.Env = env
	cmd.Dir = r.cwd
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	setProcessGroup(cmd)
	return cmd.Run()
}

// envFor returns the shell environment for a hook invocation: the
// caller's existing environment plus CODEX_HOOK_* keys describing the
// session and event-specific payload.
func (r *Runner) envFor(event string, extras map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"CODEX_HOOK_EVENT="+event,
		"CODEX_HOOK_SESSION_ID="+r.sessionID,
		"CODEX_HOOK_CWD="+r.cwd,
		"CODEX_HOOK_TRANSCRIPT_PATH="+r.transcriptPath,
		"CODEX_HOOK_PERMISSION_MODE="+r.permissionMode,
	)
	for k, v := range extras {
		env = append(env, k+"="+v)
	}
	return env
}

// writeDebug emits one trace line per command attempt. Plain text, no
// ANSI styling, mirrors internal/hooks's debug shape.
func (r *Runner) writeDebug(event, command string, elapsed time.Duration, runErr error) {
	cmd := command
	if len(cmd) > 80 {
		cmd = cmd[:77] + "..."
	}
	status := "OK"
	if runErr != nil {
		status = "ERR"
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "hook %s CMD %q %s %s", event, cmd, status, elapsed.Truncate(time.Millisecond))
	if runErr != nil {
		fmt.Fprintf(&buf, " err=%q", runErr.Error())
	}
	fmt.Fprintln(r.debugWriter, buf.String())
}

func boolToString(b bool) string { return strconv.FormatBool(b) }
