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
// Wires seven events:
//
//   - session_start
//   - user_prompt_submit
//   - pre_tool_use (CODEX_HOOK_TOOL_NAME / TOOL_INPUT / TOOL_USE_ID)
//   - post_tool_use (also CODEX_HOOK_TOOL_RESPONSE / DURATION_MS)
//   - stop
//   - pre_compact (trigger=manual|auto via CODEX_HOOK_TRIGGER)
//   - post_compact (same trigger)
//
// Codex's HooksTable has no session_end; OnSessionEnd is a no-op
// (the engine still calls it for parity with the claude path).
// Final-process teardown drains async matchers via Runner.Close —
// see its doc for the lifecycle distinction.
package codexhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paultyng/testagent/internal/engine"
	"github.com/paultyng/testagent/internal/hookresult"
	"github.com/paultyng/testagent/internal/shellrun"
	"github.com/paultyng/testagent/internal/slash"
)

// maxResponseBody caps stdout/stderr capture per matcher. Decision JSON
// is small; protects against a misbehaving hook script.
const maxResponseBody = 64 << 10

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
	EventSessionStart      = "session_start"
	EventUserPromptSubmit  = "user_prompt_submit"
	EventPreToolUse        = "pre_tool_use"
	EventPostToolUse       = "post_tool_use"
	EventStop              = "stop"
	EventPreCompact        = "pre_compact"
	EventPostCompact       = "post_compact"
	EventPermissionRequest = "permission_request"
)

// defaultTimeout caps a synchronous matcher's wall-clock when the TOML
// doesn't specify one. Mirrors the conservative default Claude's HTTP
// hooks ship with.
const defaultTimeout = 10 * time.Second

// permissionRequestTimeout is the default per-matcher wall-clock for
// permission_request, matching the 120s hold-open budget claude's
// reference implementations use. Matchers may shorten via Timeout.
const permissionRequestTimeout = 120 * time.Second

// defaultTimeoutFor returns the per-matcher default wall-clock for
// event. permission_request gets a longer ceiling because the hook
// typically holds the connection open until the user resolves the
// prompt; every other event uses the standard 10s default.
func defaultTimeoutFor(event string) time.Duration {
	if event == EventPermissionRequest {
		return permissionRequestTimeout
	}
	return defaultTimeout
}

// shutdownGracePeriod caps how long Close waits for in-flight async
// hook goroutines to finish their per-matcher timeout. Keeps process
// exit bounded even if a misbehaving hook ignores its SIGKILL'd shell.
const shutdownGracePeriod = 5 * time.Second

// Matcher is one runnable command-type hook entry, post-flattening
// from cmd/codex.Config's MatcherGroup + hooks[] schema. Pattern is
// the MatcherGroup's tool-name filter (empty/`*` = match all; literal
// = exact; `A|B|C` = any-of; otherwise regex). Only consulted for
// tool-scoped events (pre_tool_use / post_tool_use / permission_request);
// every other event ignores it.
type Matcher struct {
	Pattern string
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
// /clear or /compact — async hooks should outlive a soft-reset cycle
// and continue to the natural per-matcher timeout. After Close, fire
// becomes a no-op for async matchers (synchronous matchers still run).
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
	_, err := r.fire(ctx, EventUserPromptSubmit, "", map[string]string{
		"CODEX_HOOK_PROMPT":        prompt,
		"CODEX_HOOK_SESSION_TITLE": sessionTitle,
	})
	return err
}

// OnPreToolUse fires pre_tool_use before the tool runs. Tool input lands
// as a JSON string in CODEX_HOOK_TOOL_INPUT so matcher configs can branch
// on it via standard jq-style processing inside the hook script. The
// returned hookresult.Result carries the aggregated decision; see the
// claude OnPreToolUse doc for the contract.
func (r *Runner) OnPreToolUse(ctx context.Context, toolUseID, toolName string, toolInput any) (hookresult.Result, error) {
	return r.fire(ctx, EventPreToolUse, toolName, map[string]string{
		"CODEX_HOOK_TOOL_USE_ID": toolUseID,
		"CODEX_HOOK_TOOL_NAME":   toolName,
		"CODEX_HOOK_TOOL_INPUT":  jsonEncodeOrEmpty(toolInput),
	})
}

// OnPostToolUse fires post_tool_use after the tool completes.
func (r *Runner) OnPostToolUse(ctx context.Context, toolUseID, toolName string, toolInput, toolResponse any, durationMs int64) error {
	_, err := r.fire(ctx, EventPostToolUse, toolName, map[string]string{
		"CODEX_HOOK_TOOL_USE_ID":   toolUseID,
		"CODEX_HOOK_TOOL_NAME":     toolName,
		"CODEX_HOOK_TOOL_INPUT":    jsonEncodeOrEmpty(toolInput),
		"CODEX_HOOK_TOOL_RESPONSE": jsonEncodeOrEmpty(toolResponse),
		"CODEX_HOOK_DURATION_MS":   strconv.FormatInt(durationMs, 10),
	})
	return err
}

// OnStop fires stop hooks.
func (r *Runner) OnStop(ctx context.Context, lastAssistantMessage string, stopHookActive bool) error {
	_, err := r.fire(ctx, EventStop, "", map[string]string{
		"CODEX_HOOK_LAST_ASSISTANT_MESSAGE": lastAssistantMessage,
		"CODEX_HOOK_STOP_HOOK_ACTIVE":       boolToString(stopHookActive),
	})
	return err
}

// OnSessionStart fires session_start hooks.
func (r *Runner) OnSessionStart(ctx context.Context, source string) error {
	_, err := r.fire(ctx, EventSessionStart, "", map[string]string{
		"CODEX_HOOK_SOURCE": source,
	})
	return err
}

// OnSessionEnd is a no-op — codex's HooksTable has no session_end
// event. Engine still calls it on shutdown and on the /clear or
// /compact lifecycle for parity with the claude path; the runner's
// terminal-teardown work happens in Close, so the back-to-back
// OnSessionEnd → OnSessionStart pair never invalidates the runner.
func (r *Runner) OnSessionEnd(ctx context.Context, reason string) error {
	return nil
}

// OnPreCompact fires pre_compact before context-summarization runs.
// trigger is "manual" (user typed /compact) or "auto" (auto-compact
// lifecycle); it lands in CODEX_HOOK_TRIGGER so a config matcher on
// `trigger = "manual"` or `trigger = "auto"` can branch.
func (r *Runner) OnPreCompact(ctx context.Context, trigger string) error {
	_, err := r.fire(ctx, EventPreCompact, "", map[string]string{
		"CODEX_HOOK_TRIGGER": trigger,
	})
	return err
}

// OnPostCompact fires post_compact after the SessionStart that follows
// compaction. trigger matches the PreCompact value.
func (r *Runner) OnPostCompact(ctx context.Context, trigger string) error {
	_, err := r.fire(ctx, EventPostCompact, "", map[string]string{
		"CODEX_HOOK_TRIGGER": trigger,
	})
	return err
}

// OnPermissionRequest fires permission_request and waits for the
// matcher script to return an allow/deny decision (default per-matcher
// timeout 120s, matching the claude reference). The script returns its
// decision via stdout JSON (exit 0) or via exit 2 with stderr as the
// deny reason; see internal/hookresult for the contract. Aggregation
// is any-deny-wins, otherwise last-allow-wins.
func (r *Runner) OnPermissionRequest(ctx context.Context, toolUseID, toolName string, toolInput any) (hookresult.Result, error) {
	return r.fire(ctx, EventPermissionRequest, toolName, map[string]string{
		"CODEX_HOOK_TOOL_USE_ID": toolUseID,
		"CODEX_HOOK_TOOL_NAME":   toolName,
		"CODEX_HOOK_TOOL_INPUT":  jsonEncodeOrEmpty(toolInput),
	})
}

// fire runs every matcher registered for event. Synchronous matchers
// honor their timeout; async matchers fire-and-forget on a goroutine
// (errors are logged via debugWriter only). Per-matcher errors are
// aggregated via errors.Join — one bad matcher does not stop the rest.
//
// filterAxis is the tool name for tool-scoped events; empty for events
// with no scoping axis (every matcher fires). See hooks.matchesMatcher
// in internal/hooks for the supported pattern grammar.
//
// Synchronous matchers' stdout/stderr + exit code feed hookresult.Aggregate
// for the event; async matchers never contribute to the decision (their
// result is observed only via debugWriter, since by definition the agent
// has already moved on).
func (r *Runner) fire(ctx context.Context, event, filterAxis string, extraEnv map[string]string) (hookresult.Result, error) {
	matchers, ok := r.matchers[event]
	if !ok || len(matchers) == 0 {
		return hookresult.Result{}, nil
	}
	baseEnv := r.envFor(event, extraEnv)
	var (
		errs    []error
		results []hookresult.Result
	)
	for _, m := range matchers {
		if filterAxis != "" && !matchesMatcher(m.Pattern, filterAxis) {
			continue
		}
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
		res, err := r.runOne(ctx, event, m, baseEnv)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s hook: %w", event, err))
			continue
		}
		results = append(results, res)
	}
	return hookresult.Aggregate(event, results), errors.Join(errs...)
}

// runAsync is the goroutine body for async matchers. The per-matcher
// timeout in runOne bounds wall-clock; Close waits for these via
// inflight.Wait so they don't outlive the process. The decision result
// is discarded — async matchers cannot influence a tool's execution
// because the synchronous code path has already proceeded.
func (r *Runner) runAsync(event string, m Matcher, env []string) {
	defer r.inflight.Done()
	_, _ = r.runOne(context.Background(), event, m, env)
}

// runOne spawns the platform default shell via defaultShellCommand,
// applies the per-matcher timeout, captures stdout/stderr (capped at
// maxResponseBody), and feeds the outcome to hookresult.ParseCommand
// per the documented exit-code contract (0 = parse stdout, 2 = block
// with stderr message, other = non-blocking). Emits a debug line when
// debugWriter is set.
func (r *Runner) runOne(ctx context.Context, event string, m Matcher, env []string) (result hookresult.Result, err error) {
	start := time.Now()
	defer func() {
		if r.debugWriter != nil {
			r.writeDebug(event, m.Command, time.Since(start), err)
		}
	}()

	timeout := time.Duration(m.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutFor(event)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := shellrun.DefaultShellCommand(runCtx, m.Command)
	cmd.Env = env
	cmd.Dir = r.cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, max: maxResponseBody}
	cmd.Stderr = &limitedWriter{w: &stderr, max: maxResponseBody}
	shellrun.SetProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return hookresult.Result{}, err
	}
	cleanup := shellrun.AfterStart(cmd)
	defer cleanup()
	waitErr := cmd.Wait()
	if ctxErr := runCtx.Err(); ctxErr != nil {
		return hookresult.Result{}, ctxErr
	}
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return hookresult.Result{}, waitErr
		}
	}
	return hookresult.ParseCommand(exitCode, stdout.Bytes(), stderr.Bytes()), nil
}

// matchesMatcher mirrors hooks.matchesMatcher for codex's matcher
// patterns. Duplicated rather than imported because codex shouldn't
// depend on the claude-vendor package; the grammar is small enough
// that occasional drift is easier to spot than a cross-import.
//
//   - "" or "*"  → catch-all
//   - "shell"    → exact match
//   - "A|B|C"    → any-of alternation (segments are exact)
//   - other      → regex (unanchored substring)
func matchesMatcher(pattern, value string) bool {
	switch pattern {
	case "", "*":
		return true
	}
	if strings.Contains(pattern, "|") && !strings.ContainsAny(pattern, "().[]*+?^$\\{}") {
		for _, seg := range strings.Split(pattern, "|") {
			if seg == value {
				return true
			}
		}
		return false
	}
	if !strings.ContainsAny(pattern, "().[]*+?^$\\{}|") {
		return pattern == value
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.FindString(value) != ""
}

// limitedWriter caps memory growth from a hook that emits unbounded
// output. Mirrors internal/hooks.limitedWriter; kept separate so each
// vendor package owns its own state without cross-import.
type limitedWriter struct {
	w   io.Writer
	max int
	n   int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n >= l.max {
		return len(p), nil
	}
	remaining := l.max - l.n
	if len(p) <= remaining {
		n, err := l.w.Write(p)
		l.n += n
		return n, err
	}
	n, err := l.w.Write(p[:remaining])
	l.n += n
	if err != nil {
		return n, err
	}
	return len(p), nil
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

// jsonEncodeOrEmpty returns v marshaled to JSON, or "" when v is nil or
// marshal fails. Used so a missing tool input/response lands as an empty
// CODEX_HOOK_* env value instead of the literal Go string "<nil>".
func jsonEncodeOrEmpty(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
