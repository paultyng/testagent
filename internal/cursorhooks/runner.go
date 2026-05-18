package cursorhooks

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
	"time"

	"github.com/paultyng/testagent/internal/engine"
	"github.com/paultyng/testagent/internal/hookresult"
	"github.com/paultyng/testagent/internal/shellrun"
	"github.com/paultyng/testagent/internal/slash"
)

// maxResponseBody caps stdout/stderr capture per matcher.
const maxResponseBody = 64 << 10

// defaultTimeout caps a synchronous matcher's wall-clock when the config
// doesn't specify one.
const defaultTimeout = 10 * time.Second

// Compile-time conformance assertions.
var (
	_ engine.HookSender    = (*Runner)(nil)
	_ slash.ToolHookSender = (*Runner)(nil)
)

// Matcher is one runnable hook entry. Pattern filters tool-name for
// tool-scoped events ("" or "*" = match all; "Shell" = exact;
// "A|B|C" = any-of; otherwise interpreted as regex). Type is "command"
// by default; "prompt" entries are accepted in config but never fired
// because testagent has no LLM evaluator. Timeout is per-matcher wall-clock
// in seconds (0 → defaultTimeout). FailClosed, when true, treats a
// non-zero non-2 exit as a blocking denial instead of a non-blocking error.
type Matcher struct {
	Pattern    string
	Command    string
	Type       string // "command" (default) or "prompt"
	Timeout    int    // seconds; 0 → defaultTimeout
	LoopLimit  *int   // nil = use default 5 for loop-controlled events
	FailClosed bool
}

// Runner fires shell-command hooks for Cursor events.
type Runner struct {
	matchers       map[string][]Matcher
	sessionID      string
	cwd            string
	transcriptPath string
	permissionMode string

	// debugWriter, when non-nil, receives one line per command attempt.
	// Set to os.Stderr by --verbose. Plain text, no ANSI styling.
	debugWriter io.Writer
}

// NewRunner returns a Runner wired to the given matcher map. matchers may
// be nil (no-op runner) or omit any Cursor events. sessionID, cwd,
// transcriptPath, and permissionMode are passed to every shell command via
// CURSOR_HOOK_* env vars. debugWriter is optional; nil silences trace output.
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

// Close is a no-op for Cursor runners. Cursor's hook schema has no async
// field, so there are no in-flight goroutines to drain. Satisfies the
// engine teardown contract.
func (r *Runner) Close(_ context.Context) error {
	return nil
}

// OnPrompt is a no-op — no Cursor event models prompt-submit semantics.
func (r *Runner) OnPrompt(_ context.Context, _, _ string) error {
	return nil
}

// OnPreToolUse fires preToolUse for every tool, plus the tool-specific gating
// event when applicable (beforeShellExecution for Shell, beforeReadFile for
// Read, beforeMCPExecution for mcp__* tools). All results are aggregated into
// a single hookresult.Result via PreToolUse rules.
func (r *Runner) OnPreToolUse(ctx context.Context, toolUseID, toolName string, toolInput any) (hookresult.Result, error) {
	events := []string{EventPreToolUse}
	switch {
	case toolName == "Shell":
		events = append(events, EventBeforeShellExecution)
	case toolName == "Read":
		events = append(events, EventBeforeReadFile)
	case strings.HasPrefix(toolName, "mcp__"):
		events = append(events, EventBeforeMCPExecution)
	}
	extras := map[string]string{
		"CURSOR_HOOK_TOOL_USE_ID": toolUseID,
		"CURSOR_HOOK_TOOL_NAME":   toolName,
		"CURSOR_HOOK_TOOL_INPUT":  jsonEncodeOrEmpty(toolInput),
	}
	return r.fireMulti(ctx, events, toolName, extras)
}

// OnPostToolUse fires the appropriate advisory after-event for the tool.
// Shell → afterShellExecution; Edit → afterFileEdit; mcp__* → afterMCPExecution.
// No event fires for other tool names. Always returns nil error.
func (r *Runner) OnPostToolUse(ctx context.Context, toolUseID, toolName string, toolInput, toolResponse any, durationMs int64) error {
	var event string
	switch {
	case toolName == "Shell":
		event = EventAfterShellExecution
	case toolName == "Edit":
		event = EventAfterFileEdit
	case strings.HasPrefix(toolName, "mcp__"):
		event = EventAfterMCPExecution
	default:
		return nil
	}
	extras := map[string]string{
		"CURSOR_HOOK_TOOL_USE_ID":   toolUseID,
		"CURSOR_HOOK_TOOL_NAME":     toolName,
		"CURSOR_HOOK_TOOL_INPUT":    jsonEncodeOrEmpty(toolInput),
		"CURSOR_HOOK_TOOL_RESPONSE": jsonEncodeOrEmpty(toolResponse),
		"CURSOR_HOOK_DURATION_MS":   strconv.FormatInt(durationMs, 10),
	}
	_, err := r.fire(ctx, event, toolName, extras)
	return err
}

// OnPermissionRequest routes to preToolUse gating — Cursor models permission
// decisions as the pre-tool gate, not a separate event.
func (r *Runner) OnPermissionRequest(ctx context.Context, toolUseID, toolName string, toolInput any) (hookresult.Result, error) {
	return r.fire(ctx, EventPreToolUse, toolName, map[string]string{
		"CURSOR_HOOK_TOOL_USE_ID": toolUseID,
		"CURSOR_HOOK_TOOL_NAME":   toolName,
		"CURSOR_HOOK_TOOL_INPUT":  jsonEncodeOrEmpty(toolInput),
	})
}

// OnStop fires stop hooks.
func (r *Runner) OnStop(ctx context.Context, lastAssistantMessage string, stopHookActive bool) error {
	_, err := r.fire(ctx, EventStop, "", map[string]string{
		"CURSOR_HOOK_LAST_ASSISTANT_MESSAGE": lastAssistantMessage,
		"CURSOR_HOOK_STOP_HOOK_ACTIVE":       boolToString(stopHookActive),
	})
	return err
}

// OnSessionStart is a no-op — Cursor has no documented session_start event.
func (r *Runner) OnSessionStart(_ context.Context, _ string) error {
	return nil
}

// OnSessionEnd is a no-op — Cursor has no documented session_end event.
func (r *Runner) OnSessionEnd(_ context.Context, _ string) error {
	return nil
}

// OnPreCompact is a no-op — Cursor has no compact lifecycle event.
func (r *Runner) OnPreCompact(_ context.Context, _ string) error {
	return nil
}

// OnPostCompact is a no-op — Cursor has no compact lifecycle event.
func (r *Runner) OnPostCompact(_ context.Context, _ string) error {
	return nil
}

// fireMulti fires multiple events in order, collecting all per-matcher
// Results, then aggregates them once with PreToolUse rules. Used by
// OnPreToolUse to combine the catch-all preToolUse gate with tool-specific
// gates (beforeShellExecution, etc.).
func (r *Runner) fireMulti(ctx context.Context, events []string, filterAxis string, extraEnv map[string]string) (hookresult.Result, error) {
	var (
		allResults []hookresult.Result
		allErrs    []error
	)
	for _, event := range events {
		matchers, ok := r.matchers[event]
		if !ok || len(matchers) == 0 {
			continue
		}
		baseEnv := r.envFor(event, extraEnv)
		for _, m := range matchers {
			if filterAxis != "" && !matchesMatcher(m.Pattern, filterAxis) {
				continue
			}
			if m.Type == "prompt" {
				r.writeDebugMsg(event, m.Command, "skipped prompt-type hook")
				continue
			}
			res, err := r.runOne(ctx, event, m, baseEnv)
			if err != nil {
				allErrs = append(allErrs, fmt.Errorf("%s hook: %w", event, err))
				continue
			}
			allResults = append(allResults, res)
		}
	}
	return hookresult.Aggregate(EventPreToolUse, allResults), errors.Join(allErrs...)
}

// fire runs every matcher registered for event. filterAxis is the tool name
// for tool-scoped events; empty for events with no scoping axis. Per-matcher
// errors are aggregated via errors.Join.
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
		if m.Type == "prompt" {
			r.writeDebugMsg(event, m.Command, "skipped prompt-type hook")
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

// runOne spawns the platform default shell, applies the per-matcher timeout,
// captures stdout/stderr (capped at maxResponseBody), and feeds the outcome
// to hookresult.ParseCommand. When m.FailClosed is true and the command exits
// non-zero with a non-2 code (i.e., a normally non-blocking error), the result
// is promoted to a blocking denial.
func (r *Runner) runOne(ctx context.Context, event string, m Matcher, env []string) (result hookresult.Result, err error) {
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
	res := hookresult.ParseCommand(exitCode, stdout.Bytes(), stderr.Bytes())
	// FailClosed: non-zero non-2 exit that would normally be a non-blocking
	// error becomes a blocking denial.
	if !res.Block && exitCode != 0 && exitCode != 2 && m.FailClosed {
		return hookresult.Result{Block: true, Reason: "hook failed (failClosed)"}, nil
	}
	return res, nil
}

// matchesMatcher reports whether pattern matches value using the same grammar
// as codex:
//   - "" or "*"  → catch-all
//   - "Shell"    → exact match
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

// limitedWriter caps memory growth from a hook that emits unbounded output.
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

// envFor returns the shell environment for a hook invocation: the caller's
// existing environment plus CURSOR_HOOK_* keys describing the session and
// event-specific payload.
func (r *Runner) envFor(event string, extras map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"CURSOR_HOOK_EVENT="+event,
		"CURSOR_HOOK_SESSION_ID="+r.sessionID,
		"CURSOR_HOOK_CWD="+r.cwd,
		"CURSOR_HOOK_TRANSCRIPT_PATH="+r.transcriptPath,
		"CURSOR_HOOK_PERMISSION_MODE="+r.permissionMode,
	)
	for k, v := range extras {
		env = append(env, k+"="+v)
	}
	return env
}

// writeDebug emits one trace line per command attempt. Plain text, no ANSI.
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

// writeDebugMsg emits a single message line to debugWriter when set.
func (r *Runner) writeDebugMsg(event, command, msg string) {
	if r.debugWriter == nil {
		return
	}
	cmd := command
	if len(cmd) > 80 {
		cmd = cmd[:77] + "..."
	}
	fmt.Fprintf(r.debugWriter, "hook %s CMD %q %s\n", event, cmd, msg)
}

func boolToString(b bool) string { return strconv.FormatBool(b) }

// jsonEncodeOrEmpty returns v marshaled to JSON, or "" when v is nil or
// marshal fails.
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
