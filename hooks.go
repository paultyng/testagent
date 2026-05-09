package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Claude Code hook event names.
const (
	hookEventUserPromptSubmit = "UserPromptSubmit"
	hookEventPostToolUse      = "PostToolUse"
	hookEventStop             = "Stop"
	hookEventSessionStart     = "SessionStart"
	hookEventSessionEnd       = "SessionEnd"
)

// defaultHookTimeout is used when a Hook in settings does not specify Timeout.
const defaultHookTimeout = 10 * time.Second

// HookSender posts Claude-Code-shaped hook events to URLs declared in Settings.
type HookSender struct {
	settings       *Settings
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

// NewHookSender returns a sender wired to the given settings. Settings may be
// nil (no-op sender). sessionID is the value emitted in the body's session_id
// field. cwd/transcriptPath/permissionMode populate every event body.
// debugWriter is optional; nil disables verbose logging.
func NewHookSender(settings *Settings, sessionID, cwd, transcriptPath, permissionMode string, debugWriter io.Writer) *HookSender {
	return &HookSender{
		settings:       settings,
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
func (h *HookSender) OnPrompt(ctx context.Context, prompt, sessionTitle string) error {
	body := userPromptSubmitBody{
		CWD:            h.cwd,
		HookEventName:  hookEventUserPromptSubmit,
		PermissionMode: h.permissionMode,
		Prompt:         prompt,
		SessionID:      h.sessionID,
		SessionTitle:   sessionTitle,
		TranscriptPath: h.transcriptPath,
	}
	return h.fire(ctx, hookEventUserPromptSubmit, body)
}

// OnToolUse fires PostToolUse.
func (h *HookSender) OnToolUse(ctx context.Context, toolUseID, toolName string, toolInput, toolResponse any, durationMs int64) error {
	body := postToolUseBody{
		CWD:            h.cwd,
		DurationMs:     durationMs,
		HookEventName:  hookEventPostToolUse,
		PermissionMode: h.permissionMode,
		SessionID:      h.sessionID,
		ToolInput:      toolInput,
		ToolName:       toolName,
		ToolResponse:   toolResponse,
		ToolUseID:      toolUseID,
		TranscriptPath: h.transcriptPath,
	}
	return h.fire(ctx, hookEventPostToolUse, body)
}

// OnStop fires Stop.
func (h *HookSender) OnStop(ctx context.Context, lastAssistantMessage string, stopHookActive bool) error {
	body := stopBody{
		CWD:                  h.cwd,
		HookEventName:        hookEventStop,
		LastAssistantMessage: lastAssistantMessage,
		PermissionMode:       h.permissionMode,
		SessionID:            h.sessionID,
		StopHookActive:       stopHookActive,
		TranscriptPath:       h.transcriptPath,
	}
	return h.fire(ctx, hookEventStop, body)
}

// OnSessionStart fires SessionStart. source is one of "startup", "resume",
// "clear", "compact" — same vocabulary Claude Code uses on the matcher field.
func (h *HookSender) OnSessionStart(ctx context.Context, source string) error {
	body := sessionStartBody{
		CWD:            h.cwd,
		HookEventName:  hookEventSessionStart,
		SessionID:      h.sessionID,
		Source:         source,
		TranscriptPath: h.transcriptPath,
	}
	return h.fire(ctx, hookEventSessionStart, body)
}

// OnSessionEnd fires SessionEnd. reason is one of "clear", "logout", "other", etc.
func (h *HookSender) OnSessionEnd(ctx context.Context, reason string) error {
	body := sessionEndBody{
		CWD:            h.cwd,
		HookEventName:  hookEventSessionEnd,
		Reason:         reason,
		SessionID:      h.sessionID,
		TranscriptPath: h.transcriptPath,
	}
	return h.fire(ctx, hookEventSessionEnd, body)
}

// fire iterates every HookMatcher registered for event and POSTs body to each
// matcher's HTTP hooks. Per-hook errors are aggregated via errors.Join; a
// failing hook does not prevent the rest from firing.
func (h *HookSender) fire(ctx context.Context, event string, body any) error {
	if h.settings == nil || len(h.settings.Hooks) == 0 {
		return nil
	}
	matchers, ok := h.settings.Hooks[event]
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
			if hook.Type != "http" {
				continue
			}
			if err := h.post(ctx, event, hook, payload); err != nil {
				errs = append(errs, fmt.Errorf("%s hook %s: %w", event, hook.URL, err))
			}
		}
	}
	return errors.Join(errs...)
}

// post POSTs payload as application/json to hook.URL with hook.Headers applied.
// hook.Timeout is honored as a per-request context deadline (0 → default).
// When debugWriter is set, emits a one-line trace per attempt regardless of
// outcome (build error, transport error, success, non-2xx).
func (h *HookSender) post(ctx context.Context, event string, hook Hook, payload []byte) (err error) {
	start := time.Now()
	status := 0
	defer func() {
		if h.debugWriter != nil {
			h.writeDebug(event, hook.URL, status, time.Since(start), len(payload), err)
		}
	}()

	timeout := time.Duration(hook.Timeout) * time.Second
	if timeout <= 0 {
		timeout = defaultHookTimeout
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
	resp, err := h.httpClient.Do(req)
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

// writeDebug emits one line to debugWriter:
//
//	hook <event> POST <url> <status|ERR> <elapsed> <bodysize> [err="..."]
//
// status 0 renders as ERR. Trailing err="..." is present only when err != nil.
// Plain text, no ANSI — verbose output is for grepping/piping.
func (h *HookSender) writeDebug(event, url string, status int, elapsed time.Duration, bodySize int, postErr error) {
	statusStr := "ERR"
	if status > 0 {
		statusStr = fmt.Sprintf("%d", status)
	}
	line := fmt.Sprintf("hook %s POST %s %s %s %s",
		event, url, statusStr, elapsed.Truncate(time.Millisecond), formatBytes(bodySize))
	if postErr != nil {
		line += fmt.Sprintf(" err=%q", postErr.Error())
	}
	fmt.Fprintln(h.debugWriter, line)
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
