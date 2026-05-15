// Package hooks fires Claude-Code-shaped hook events. Two hook types
// are supported: Type="http" posts the JSON event body to a URL, and
// Type="command" pipes the JSON event body to a shell command's stdin.
// Sender accepts a flat map of matchers keyed by event name — callers
// (e.g. cmd/claude) own the on-disk Settings struct that wraps this map.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"

	"github.com/paultyng/testagent/internal/hookresult"
	"github.com/paultyng/testagent/internal/shellrun"
)

// maxResponseBody caps the per-hook response body we read into memory.
// Decision JSON is small; protects against a misbehaving hook server that
// streams gigabytes. 64KiB is the same ceiling Claude Code applies.
const maxResponseBody = 64 << 10

// Claude Code hook event names, exported so callers can build matcher maps
// without stringly-typed event keys.
const (
	UserPromptSubmit  = "UserPromptSubmit"
	PreToolUse        = "PreToolUse"
	PostToolUse       = "PostToolUse"
	Stop              = "Stop"
	SessionStart      = "SessionStart"
	SessionEnd        = "SessionEnd"
	PreCompact        = "PreCompact"
	PostCompact       = "PostCompact"
	Notification      = "Notification"
	PermissionRequest = "PermissionRequest"
)

// Notification matcher values, mirroring Claude Code's documented set.
// Orchestrators can configure hooks scoped to any subset; testagent
// passes these through as the Matcher field in the event body.
const (
	NotificationPermissionPrompt    = "permission_prompt"
	NotificationIdlePrompt          = "idle_prompt"
	NotificationAuthSuccess         = "auth_success"
	NotificationElicitationDialog   = "elicitation_dialog"
	NotificationElicitationComplete = "elicitation_complete"
	NotificationElicitationResponse = "elicitation_response"
)

// defaultTimeout is used when a Hook in settings does not specify Timeout.
const defaultTimeout = 10 * time.Second

// permissionRequestTimeout is the default per-hook wall-clock for
// PermissionRequest, matching the 120s hold-open budget agentsd's
// reference implementation uses. Hooks may shorten it via Hook.Timeout.
const permissionRequestTimeout = 120 * time.Second

// defaultTimeoutFor returns the per-hook default wall-clock for event.
// PermissionRequest gets a longer ceiling because the server typically
// holds the connection open until the user resolves the prompt; every
// other event uses the standard 10s default.
func defaultTimeoutFor(event string) time.Duration {
	if event == PermissionRequest {
		return permissionRequestTimeout
	}
	return defaultTimeout
}

// Matcher binds a matcher pattern to one or more http hooks. Mirrors the
// shape Claude Code's settings.json uses under hooks.<event>[].
type Matcher struct {
	Matcher string `json:"matcher"`
	Hooks   []Hook `json:"hooks"`
}

// Hook is a single hook target. Type="http" POSTs the event body to URL
// with Headers applied; Type="command" pipes the event body to Command's
// stdin via the platform's default shell. Timeout (seconds) bounds the
// per-hook wall-clock for either type; 0 → defaultTimeout.
type Hook struct {
	Type    string            `json:"type"`
	URL     string            `json:"url,omitempty"`
	Command string            `json:"command,omitempty"`
	Timeout int               `json:"timeout"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Sender posts hook events to URLs declared in matchers.
type Sender struct {
	matchers       map[string][]Matcher
	sessionID      string
	cwd            string
	transcriptPath string
	permissionMode string
	httpClient     *http.Client

	// debugWriter, when non-nil, receives one line per POST attempt:
	// "hook <event> POST <url> <status|ERR> <elapsed> <bodysize> [err=...]"
	// Set to os.Stderr by --verbose. Nil silences debug output entirely.
	debugWriter io.Writer
}

// NewSender returns a sender wired to the given matcher map. matchers may be
// nil (no-op sender). sessionID is the value emitted in the body's session_id
// field. cwd/transcriptPath/permissionMode populate every event body.
// debugWriter is optional; nil disables verbose logging.
func NewSender(matchers map[string][]Matcher, sessionID, cwd, transcriptPath, permissionMode string, debugWriter io.Writer) *Sender {
	return &Sender{
		matchers:       matchers,
		sessionID:      sessionID,
		cwd:            cwd,
		transcriptPath: transcriptPath,
		permissionMode: permissionMode,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
		debugWriter:    debugWriter,
	}
}

// userPromptSubmitBody is the JSON body for UserPromptSubmit.
type userPromptSubmitBody struct {
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	PermissionMode string `json:"permission_mode"`
	Prompt         string `json:"prompt"`
	SessionID      string `json:"session_id"`
	SessionTitle   string `json:"session_title"`
	TranscriptPath string `json:"transcript_path"`
}

// preToolUseBody is the JSON body for PreToolUse. Fires before the tool
// runs; no tool_response or duration yet.
type preToolUseBody struct {
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	PermissionMode string `json:"permission_mode"`
	SessionID      string `json:"session_id"`
	ToolInput      any    `json:"tool_input"`
	ToolName       string `json:"tool_name"`
	ToolUseID      string `json:"tool_use_id"`
	TranscriptPath string `json:"transcript_path"`
}

// postToolUseBody is the JSON body for PostToolUse.
type postToolUseBody struct {
	CWD            string `json:"cwd"`
	DurationMs     int64  `json:"duration_ms"`
	HookEventName  string `json:"hook_event_name"`
	PermissionMode string `json:"permission_mode"`
	SessionID      string `json:"session_id"`
	ToolInput      any    `json:"tool_input"`
	ToolName       string `json:"tool_name"`
	ToolResponse   any    `json:"tool_response"`
	ToolUseID      string `json:"tool_use_id"`
	TranscriptPath string `json:"transcript_path"`
}

// stopBody is the JSON body for Stop.
type stopBody struct {
	CWD                  string `json:"cwd"`
	HookEventName        string `json:"hook_event_name"`
	LastAssistantMessage string `json:"last_assistant_message"`
	PermissionMode       string `json:"permission_mode"`
	SessionID            string `json:"session_id"`
	StopHookActive       bool   `json:"stop_hook_active"`
	TranscriptPath       string `json:"transcript_path"`
}

// sessionStartBody is the JSON body for SessionStart. source is one of
// "startup", "resume", "clear", "compact" — matches Claude Code's matcher
// vocabulary so orchestrators can distinguish a fresh boot from /clear
// or /compact.
type sessionStartBody struct {
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	Source         string `json:"source"`
	TranscriptPath string `json:"transcript_path"`
}

// sessionEndBody is the JSON body for SessionEnd.
type sessionEndBody struct {
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Reason         string `json:"reason"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// compactBody is the JSON body for PreCompact and PostCompact — both events
// share the same shape. Trigger is "manual" (user typed /compact) or "auto"
// (testagent's /fake-auto-compact simulating Claude's context-fill trigger).
type compactBody struct {
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Trigger        string `json:"trigger"`
}

// notificationBody is the JSON body for Notification. Claude Code fires
// Notification fire-and-forget (idle prompts, permission UI events,
// elicitation lifecycle); testagent posts the body but discards the
// response. The Matcher field carries one of the documented values
// (permission_prompt / idle_prompt / auth_success / elicitation_*) so
// orchestrator hooks can branch.
type notificationBody struct {
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Matcher        string `json:"matcher,omitempty"`
	Message        string `json:"message"`
	PermissionMode string `json:"permission_mode"`
	SessionID      string `json:"session_id"`
	Title          string `json:"title,omitempty"`
	TranscriptPath string `json:"transcript_path"`
}

// permissionRequestBody is the JSON body for PermissionRequest. The
// hook server holds the HTTP connection open until it decides allow
// or deny; testagent waits up to permissionRequestTimeout for that
// response and routes on the nested hookSpecificOutput.decision.behavior
// shape via internal/hookresult.
type permissionRequestBody struct {
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	PermissionMode string `json:"permission_mode"`
	SessionID      string `json:"session_id"`
	ToolInput      any    `json:"tool_input"`
	ToolName       string `json:"tool_name"`
	ToolUseID      string `json:"tool_use_id"`
	TranscriptPath string `json:"transcript_path"`
}

// OnPrompt fires UserPromptSubmit. sessionTitle is the human-facing label.
func (s *Sender) OnPrompt(ctx context.Context, prompt, sessionTitle string) error {
	body := userPromptSubmitBody{
		CWD:            s.cwd,
		HookEventName:  UserPromptSubmit,
		PermissionMode: s.permissionMode,
		Prompt:         prompt,
		SessionID:      s.sessionID,
		SessionTitle:   sessionTitle,
		TranscriptPath: s.transcriptPath,
	}
	_, err := s.fire(ctx, UserPromptSubmit, body)
	return err
}

// OnPreToolUse fires PreToolUse before the tool runs. The returned
// hookresult.Result carries the aggregated decision the hook server(s)
// returned (block / ask / allow + reason). Callers that gate tool
// execution on the response — currently the slash dispatcher — consult
// Block and Ask; other callers may discard it.
func (s *Sender) OnPreToolUse(ctx context.Context, toolUseID, toolName string, toolInput any) (hookresult.Result, error) {
	body := preToolUseBody{
		CWD:            s.cwd,
		HookEventName:  PreToolUse,
		PermissionMode: s.permissionMode,
		SessionID:      s.sessionID,
		ToolInput:      toolInput,
		ToolName:       toolName,
		ToolUseID:      toolUseID,
		TranscriptPath: s.transcriptPath,
	}
	return s.fire(ctx, PreToolUse, body)
}

// OnPostToolUse fires PostToolUse after the tool completes.
func (s *Sender) OnPostToolUse(ctx context.Context, toolUseID, toolName string, toolInput, toolResponse any, durationMs int64) error {
	body := postToolUseBody{
		CWD:            s.cwd,
		DurationMs:     durationMs,
		HookEventName:  PostToolUse,
		PermissionMode: s.permissionMode,
		SessionID:      s.sessionID,
		ToolInput:      toolInput,
		ToolName:       toolName,
		ToolResponse:   toolResponse,
		ToolUseID:      toolUseID,
		TranscriptPath: s.transcriptPath,
	}
	_, err := s.fire(ctx, PostToolUse, body)
	return err
}

// OnStop fires Stop.
func (s *Sender) OnStop(ctx context.Context, lastAssistantMessage string, stopHookActive bool) error {
	body := stopBody{
		CWD:                  s.cwd,
		HookEventName:        Stop,
		LastAssistantMessage: lastAssistantMessage,
		PermissionMode:       s.permissionMode,
		SessionID:            s.sessionID,
		StopHookActive:       stopHookActive,
		TranscriptPath:       s.transcriptPath,
	}
	_, err := s.fire(ctx, Stop, body)
	return err
}

// OnSessionStart fires SessionStart. source is one of "startup", "resume",
// "clear", "compact" — same vocabulary Claude Code uses on the matcher field.
func (s *Sender) OnSessionStart(ctx context.Context, source string) error {
	body := sessionStartBody{
		CWD:            s.cwd,
		HookEventName:  SessionStart,
		SessionID:      s.sessionID,
		Source:         source,
		TranscriptPath: s.transcriptPath,
	}
	_, err := s.fire(ctx, SessionStart, body)
	return err
}

// OnSessionEnd fires SessionEnd. reason is one of "clear", "logout", "other", etc.
func (s *Sender) OnSessionEnd(ctx context.Context, reason string) error {
	body := sessionEndBody{
		CWD:            s.cwd,
		HookEventName:  SessionEnd,
		Reason:         reason,
		SessionID:      s.sessionID,
		TranscriptPath: s.transcriptPath,
	}
	_, err := s.fire(ctx, SessionEnd, body)
	return err
}

// OnPreCompact fires PreCompact before context-summarization runs. trigger
// is "manual" (user typed /compact) or "auto" (auto-compact lifecycle).
func (s *Sender) OnPreCompact(ctx context.Context, trigger string) error {
	body := compactBody{
		CWD:            s.cwd,
		HookEventName:  PreCompact,
		SessionID:      s.sessionID,
		TranscriptPath: s.transcriptPath,
		Trigger:        trigger,
	}
	_, err := s.fire(ctx, PreCompact, body)
	return err
}

// OnPostCompact fires PostCompact after the SessionStart that follows
// compaction. trigger matches the PreCompact value.
func (s *Sender) OnPostCompact(ctx context.Context, trigger string) error {
	body := compactBody{
		CWD:            s.cwd,
		HookEventName:  PostCompact,
		SessionID:      s.sessionID,
		TranscriptPath: s.transcriptPath,
		Trigger:        trigger,
	}
	_, err := s.fire(ctx, PostCompact, body)
	return err
}

// OnNotification fires Notification. Claude Code uses this for idle
// prompts, permission UI events, and the elicitation lifecycle — all
// advisory. matcher is one of the documented values (use the
// Notification* constants); message and title are user-facing strings.
// Any decision returned by the hook is discarded (Notification does
// not gate anything).
func (s *Sender) OnNotification(ctx context.Context, matcher, message, title string) error {
	body := notificationBody{
		CWD:            s.cwd,
		HookEventName:  Notification,
		Matcher:        matcher,
		Message:        message,
		PermissionMode: s.permissionMode,
		SessionID:      s.sessionID,
		Title:          title,
		TranscriptPath: s.transcriptPath,
	}
	_, err := s.fire(ctx, Notification, body)
	return err
}

// OnPermissionRequest fires PermissionRequest and waits for the hook
// server to return an allow/deny decision (default per-hook timeout
// 120s, matching agentsd's reference). The returned hookresult.Result
// carries the aggregated behavior; aggregation is any-deny-wins,
// otherwise last-allow-wins (see internal/hookresult).
func (s *Sender) OnPermissionRequest(ctx context.Context, toolUseID, toolName string, toolInput any) (hookresult.Result, error) {
	body := permissionRequestBody{
		CWD:            s.cwd,
		HookEventName:  PermissionRequest,
		PermissionMode: s.permissionMode,
		SessionID:      s.sessionID,
		ToolInput:      toolInput,
		ToolName:       toolName,
		ToolUseID:      toolUseID,
		TranscriptPath: s.transcriptPath,
	}
	return s.fire(ctx, PermissionRequest, body)
}

// fire iterates every Matcher registered for event and dispatches each
// hook to the runner for its Type ("http" or "command"). Per-hook errors
// are aggregated via errors.Join; a failing hook does not prevent the
// rest from firing. Unknown Types are silently skipped (forward-compat
// with hooks-config readers that downgrade to a no-op rather than fail).
//
// Per-hook decision bodies are parsed via hookresult and aggregated
// using the event-specific rule. The aggregated result is returned to
// the caller; callers that gate on decisions (slash dispatcher) consult
// Block / Ask / Allow, others discard it.
func (s *Sender) fire(ctx context.Context, event string, body any) (hookresult.Result, error) {
	if len(s.matchers) == 0 {
		return hookresult.Result{}, nil
	}
	matchers, ok := s.matchers[event]
	if !ok || len(matchers) == 0 {
		return hookresult.Result{}, nil
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return hookresult.Result{}, fmt.Errorf("marshaling %s body: %w", event, err)
	}
	var (
		errs    []error
		results []hookresult.Result
	)
	for _, m := range matchers {
		for _, hook := range m.Hooks {
			switch hook.Type {
			case "http":
				r, err := s.post(ctx, event, hook, payload)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s hook %s: %w", event, hook.URL, err))
					continue
				}
				results = append(results, r)
			case "command":
				r, err := s.runCommand(ctx, event, hook, payload)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s hook %s: %w", event, hook.Command, err))
					continue
				}
				results = append(results, r)
			}
		}
	}
	return hookresult.Aggregate(event, results), errors.Join(errs...)
}

// post POSTs payload as application/json to hook.URL with hook.Headers applied.
// hook.Timeout is honored as a per-request context deadline (0 → default).
// When debugWriter is set, emits a one-line trace per attempt regardless of
// outcome (build error, transport error, success, non-2xx). On 2xx the
// response body (capped at maxResponseBody) is parsed via hookresult; the
// returned Result is zero when the body is empty or unparseable.
func (s *Sender) post(ctx context.Context, event string, hook Hook, payload []byte) (result hookresult.Result, err error) {
	start := time.Now()
	status := 0
	defer func() {
		if s.debugWriter != nil {
			s.writeDebug(event, hook.URL, status, time.Since(start), len(payload), err)
		}
	}()

	timeout := time.Duration(hook.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutFor(event)
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, hook.URL, bytes.NewReader(payload))
	if err != nil {
		return hookresult.Result{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hook.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return hookresult.Result{}, fmt.Errorf("posting: %w", err)
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	if resp.StatusCode >= 400 {
		return hookresult.Result{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if readErr != nil {
		// A truncated body invalidates the decision (parsed JSON might be
		// half-read garbage); surface the read failure to the caller and
		// let the deferred writeDebug log it with the canonical shape.
		return hookresult.Result{}, fmt.Errorf("reading response: %w", readErr)
	}
	return hookresult.ParseBody(body), nil
}

// runCommand spawns hook.Command via the platform default shell and
// pipes payload to its stdin. hook.Timeout (or defaultTimeout) bounds
// the wall-clock; on context cancel or timeout the shell + any
// grandchildren are killed via shellrun's process-group / Job-object
// machinery. stdout and stderr are captured up to maxResponseBody so
// hookresult can apply the documented exit-code contract: 0 parses
// stdout JSON; 2 blocks with stderr as the reason; other non-zero exit
// codes are non-blocking and produce a zero result.
func (s *Sender) runCommand(ctx context.Context, event string, hook Hook, payload []byte) (result hookresult.Result, err error) {
	start := time.Now()
	defer func() {
		if s.debugWriter != nil {
			s.writeCmdDebug(event, hook.Command, time.Since(start), len(payload), err)
		}
	}()

	timeout := time.Duration(hook.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutFor(event)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := shellrun.DefaultShellCommand(runCtx, hook.Command)
	cmd.Dir = s.cwd
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, max: maxResponseBody}
	cmd.Stderr = &limitedWriter{w: &stderr, max: maxResponseBody}
	shellrun.SetProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return hookresult.Result{}, fmt.Errorf("starting: %w", err)
	}
	cleanup := shellrun.AfterStart(cmd)
	defer cleanup()
	waitErr := cmd.Wait()
	if ctxErr := runCtx.Err(); ctxErr != nil {
		// Context cancel / deadline surfaces as a run error regardless of
		// how Wait reported the underlying signal kill. Preserves the
		// timeout contract callers had pre-refactor.
		return hookresult.Result{}, fmt.Errorf("running: %w", ctxErr)
	}
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return hookresult.Result{}, fmt.Errorf("running: %w", waitErr)
		}
	}
	return hookresult.ParseCommand(exitCode, stdout.Bytes(), stderr.Bytes()), nil
}

// limitedWriter writes to w until max bytes have been accepted; beyond
// that point Write silently discards. Caps memory growth from a hook
// that emits unbounded output without surfacing a write error to the
// child process (which would change its observable behavior vs real
// Claude).
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

// writeCmdDebug emits the command-hook analog of writeDebug:
//
//	hook <event> CMD <command-summary> <status|ERR> <elapsed> <bodysize> [err="..."]
//
// command-summary truncates to 80 chars so a multi-line shell pipeline
// doesn't bloat the trace line. Plain text, no ANSI.
func (s *Sender) writeCmdDebug(event, command string, elapsed time.Duration, bodySize int, runErr error) {
	cmd := command
	if len(cmd) > 80 {
		cmd = cmd[:77] + "..."
	}
	status := "OK"
	if runErr != nil {
		status = "ERR"
	}
	line := fmt.Sprintf("hook %s CMD %q %s %s %s",
		event, cmd, status, elapsed.Truncate(time.Millisecond), formatBytes(bodySize))
	if runErr != nil {
		line += fmt.Sprintf(" err=%q", runErr.Error())
	}
	fmt.Fprintln(s.debugWriter, line)
}

// writeDebug emits one line to debugWriter:
//
//	hook <event> POST <url> <status|ERR> <elapsed> <bodysize> [err="..."]
//
// status 0 renders as ERR. Trailing err="..." is present only when err != nil.
// Plain text, no ANSI — verbose output is for grepping/piping.
func (s *Sender) writeDebug(event, url string, status int, elapsed time.Duration, bodySize int, postErr error) {
	statusStr := "ERR"
	if status > 0 {
		statusStr = fmt.Sprintf("%d", status)
	}
	line := fmt.Sprintf("hook %s POST %s %s %s %s",
		event, url, statusStr, elapsed.Truncate(time.Millisecond), formatBytes(bodySize))
	if postErr != nil {
		line += fmt.Sprintf(" err=%q", postErr.Error())
	}
	fmt.Fprintln(s.debugWriter, line)
}

// formatBytes returns a compact size string: 412B / 1.2kB / 3.4MB.
func formatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fkB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}
