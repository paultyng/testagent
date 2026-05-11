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
	"time"

	"github.com/paultyng/testagent/internal/shellrun"
)

// Claude Code hook event names, exported so callers can build matcher maps
// without stringly-typed event keys.
const (
	UserPromptSubmit = "UserPromptSubmit"
	PostToolUse      = "PostToolUse"
	Stop             = "Stop"
	SessionStart     = "SessionStart"
	SessionEnd       = "SessionEnd"
)

// defaultTimeout is used when a Hook in settings does not specify Timeout.
const defaultTimeout = 10 * time.Second

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
// vocabulary so orchestrators can distinguish a fresh boot from a /restart.
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
	return s.fire(ctx, UserPromptSubmit, body)
}

// OnToolUse fires PostToolUse.
func (s *Sender) OnToolUse(ctx context.Context, toolUseID, toolName string, toolInput, toolResponse any, durationMs int64) error {
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
	return s.fire(ctx, PostToolUse, body)
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
	return s.fire(ctx, Stop, body)
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
	return s.fire(ctx, SessionStart, body)
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
	return s.fire(ctx, SessionEnd, body)
}

// fire iterates every Matcher registered for event and dispatches each
// hook to the runner for its Type ("http" or "command"). Per-hook errors
// are aggregated via errors.Join; a failing hook does not prevent the
// rest from firing. Unknown Types are silently skipped (forward-compat
// with hooks-config readers that downgrade to a no-op rather than fail).
func (s *Sender) fire(ctx context.Context, event string, body any) error {
	if len(s.matchers) == 0 {
		return nil
	}
	matchers, ok := s.matchers[event]
	if !ok || len(matchers) == 0 {
		return nil
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling %s body: %w", event, err)
	}
	var errs []error
	for _, m := range matchers {
		for _, hook := range m.Hooks {
			switch hook.Type {
			case "http":
				if err := s.post(ctx, event, hook, payload); err != nil {
					errs = append(errs, fmt.Errorf("%s hook %s: %w", event, hook.URL, err))
				}
			case "command":
				if err := s.runCommand(ctx, event, hook, payload); err != nil {
					errs = append(errs, fmt.Errorf("%s hook %s: %w", event, hook.Command, err))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// post POSTs payload as application/json to hook.URL with hook.Headers applied.
// hook.Timeout is honored as a per-request context deadline (0 → default).
// When debugWriter is set, emits a one-line trace per attempt regardless of
// outcome (build error, transport error, success, non-2xx).
func (s *Sender) post(ctx context.Context, event string, hook Hook, payload []byte) (err error) {
	start := time.Now()
	status := 0
	defer func() {
		if s.debugWriter != nil {
			s.writeDebug(event, hook.URL, status, time.Since(start), len(payload), err)
		}
	}()

	timeout := time.Duration(hook.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, hook.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hook.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting: %w", err)
	}
	defer resp.Body.Close()
	status = resp.StatusCode
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// runCommand spawns hook.Command via the platform default shell and
// pipes payload to its stdin. hook.Timeout (or defaultTimeout) bounds
// the wall-clock; on context cancel or timeout the shell + any
// grandchildren are killed via shellrun's process-group / Job-object
// machinery. stdout/stderr are discarded — the hook's effect is its
// stdin-driven side effects, not its console output.
func (s *Sender) runCommand(ctx context.Context, event string, hook Hook, payload []byte) (err error) {
	start := time.Now()
	defer func() {
		if s.debugWriter != nil {
			s.writeCmdDebug(event, hook.Command, time.Since(start), len(payload), err)
		}
	}()

	timeout := time.Duration(hook.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := shellrun.DefaultShellCommand(runCtx, hook.Command)
	cmd.Dir = s.cwd
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	shellrun.SetProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting: %w", err)
	}
	cleanup := shellrun.AfterStart(cmd)
	defer cleanup()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("running: %w", err)
	}
	return nil
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
