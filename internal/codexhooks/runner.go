// Package codexhooks runs Codex-shaped hooks. Codex's hook handlers are
// shell command strings configured under [hooks] in ~/.codex/config.toml,
// not HTTP POSTs like Claude's. The Runner satisfies the same
// engine.HookSender / slash.ToolHookSender interfaces internal/hooks
// satisfies, so vendor selection is just choosing which struct to
// build at the cmd/codex layer.
//
// Commands run via the platform's default shell, mirroring upstream
// codex's `default_shell_command`: $SHELL -lc <cmd> on Unix (fallback
// /bin/sh) and %COMSPEC% /C <cmd> on Windows (fallback cmd.exe). The
// $SHELL / %COMSPEC% env vars are honored so users get their configured
// shell.
//
// MVP wires three events from #13:
//
//   - session_start
//   - user_prompt_submit
//   - stop
//
// Codex's HooksTable has no session_end; OnSessionEnd is a no-op
// (the engine still calls it for parity with the claude path).
// Final-process teardown drains async matchers via Runner.Close —
// see its doc for the lifecycle distinction.
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
	"strconv"
	"sync"
	"time"

	"github.com/paultyng/testagent/internal/engine"
	"github.com/paultyng/testagent/internal/slash"
)

// Compile-time conformance: Runner satisfies both interfaces the
// engine and slash dispatcher consume. Keeping the assertions here
// rather than at the consumer site mirrors the *hooks.Sender pattern
// while pinning conformance to this package's own changes.
var (
	_ engine.HookSender    = (*Runner)(nil)
	_ slash.ToolHookSender = (*Runner)(nil)
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

// shutdownGracePeriod caps how long Close waits for in-flight async
// hook goroutines to finish their per-matcher timeout. Keeps process
// exit bounded even if a misbehaving hook ignores its SIGKILL'd shell.
const shutdownGracePeriod = 5 * time.Second

// Matcher is one runnable command-type hook entry, post-flattening
// from cmd/codex.Config's MatcherGroup + hooks[] schema. Only the
// fields the Runner consumes appear here (the on-disk schema has
// more — see cmd/codex/config.go).
type Matcher struct {
	Command string
	Async   bool
	Timeout int // seconds; 0 → defaultTimeout
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

	// inflight counts in-flight async matcher goroutines so Close can
	// join them before process exit. mu/closed guard the Add side
	// against a race with Close: once closed is true, fire stops
	// spawning new async goroutines so the WaitGroup invariant
	// (no Add at counter=0 once Wait has begun) is preserved.
	// closeOnce makes Close idempotent — engine's shutdown closure can
	// be called from racing AutoExit / SIGINT goroutines.
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
	inflight  sync.WaitGroup
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

// Close drains any in-flight async matcher goroutines and prevents
// new ones from being spawned. Intended to be called once during
// final process teardown (engine.Run's shutdown closure), NOT on
// /restart — async hooks should outlive a restart cycle and continue
// to the natural per-matcher timeout. After Close, fire becomes a
// no-op for async matchers (synchronous matchers still run).
//
// Returns when in-flight goroutines have finished, the supplied ctx
// is cancelled, or shutdownGracePeriod has elapsed — whichever comes
// first — so process exit stays bounded even if a hook ignores its
// SIGKILL'd shell.
func (r *Runner) Close(ctx context.Context) error {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()

		waitCh := make(chan struct{})
		go func() {
			r.inflight.Wait()
			close(waitCh)
		}()
		select {
		case <-waitCh:
		case <-ctx.Done():
		case <-time.After(shutdownGracePeriod):
		}
	})
	return nil
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

// OnSessionEnd is a no-op — codex's HooksTable has no session_end
// event. Engine still calls it on shutdown (and on /restart's
// "soft end") for parity with the claude path; the runner's
// terminal-teardown work happens in Close, so /restart can fire
// OnSessionEnd → OnSessionStart without invalidating the runner.
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
			r.mu.Lock()
			if r.closed {
				r.mu.Unlock()
				continue
			}
			r.inflight.Add(1)
			r.mu.Unlock()
			go r.runAsync(event, m, baseEnv)
			continue
		}
		if err := r.runOne(ctx, event, m, baseEnv); err != nil {
			errs = append(errs, fmt.Errorf("%s hook: %w", event, err))
		}
	}
	return errors.Join(errs...)
}

// runAsync is the goroutine body for async matchers. The per-matcher
// timeout in runOne bounds wall-clock; Close waits for these via
// inflight.Wait so they don't outlive the process.
func (r *Runner) runAsync(event string, m Matcher, env []string) {
	defer r.inflight.Done()
	_ = r.runOne(context.Background(), event, m, env)
}

// runOne spawns the platform default shell via defaultShellCommand,
// applies the per-matcher timeout, and emits a debug line if
// debugWriter is set.
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

	cmd := defaultShellCommand(runCtx, m.Command)
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
