// Package engine drives the interactive testagent loop. It picks between a
// bubbletea TUI (when stdin is a TTY) and a bufio.Scanner-based fallback
// (when stdin is piped), runs the loop, fires the SessionEnd hook on quit,
// and returns the exit code.
package engine

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/slash"
)

// Globals holds the per-run configuration shared by the TUI and scanner
// loops. Values come from the caller's flag set; the engine treats them as
// immutable for the lifetime of Run.
type Globals struct {
	Emulator    string // "Claude", "Codex", etc. — vendor type prefix shown in the banner
	Name        string // user-supplied session label (--name)
	SessionID   string
	Resumed     bool
	ThinkDelay  time.Duration // default thinking-spinner duration per turn
	StreamDelay time.Duration // default per-token interval for the response stream
	ExitAfter   int
	AutoExit    time.Duration
	HistoryCap  int    // 0 = unlimited
	StatusLine  string // shown under the banner; empty = omitted
}

// HookSender is the engine's interface to vendor-specific hook delivery.
// claude's HTTP-POST sender (internal/hooks) and codex's TOML shell-
// command runner (internal/codexhooks) both satisfy it. Defined here at
// the consumer site per Go conventions.
//
// OnToolUse is included so the slash dispatcher (which fires PostToolUse
// when /fake-tool-result completes) can take a value of this same type
// rather than a separate one-method interface — Go interface assignment
// is structural, so a value held as HookSender keeps OnToolUse callable.
type HookSender interface {
	OnPrompt(ctx context.Context, prompt, sessionTitle string) error
	OnToolUse(ctx context.Context, toolUseID, toolName string, toolInput, toolResponse any, durationMs int64) error
	OnStop(ctx context.Context, lastAssistantMessage string, stopHookActive bool) error
	OnSessionStart(ctx context.Context, source string) error
	OnSessionEnd(ctx context.Context, reason string) error
}

// Compile-time check that the canonical HTTP sender satisfies the
// interface. Other implementations (e.g. internal/codexhooks.Runner)
// are conformance-checked at the assignment site in their respective
// cmd/<vendor>/ wiring.
var _ HookSender = (*hooks.Sender)(nil)

// Deps are the runtime dependencies the engine drives. All fields are
// required.
type Deps struct {
	Hooks HookSender
	MCP   *mcp.Client
	Slash *slash.Handler
}

// Run picks TUI vs scanner based on stdin TTY status, runs the loop until
// quit/exit, fires SessionEnd, closes MCP, and returns the exit code.
//
// The returned reason for SessionEnd mirrors Claude Code's vocabulary
// ("logout" for /exit, "other" for SIGINT, EOF, /auto-exit, etc.).
func Run(ctx context.Context, g Globals, d Deps) int {
	shutdown := func(reason string) {
		// Flush any in-flight /fake-tool that never got a /fake-tool-result so its
		// PostToolUse fires (with empty response) before SessionEnd.
		d.Slash.FlushPendingTool(ctx)
		if err := d.Hooks.OnSessionEnd(ctx, reason); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnSessionEnd: %v\n", err)
		}
		// Drain any in-flight async hook goroutines bounded by the
		// runner's grace period. Implementations that don't have
		// async work (HTTP sender) don't satisfy this and are skipped.
		if closer, ok := d.Hooks.(interface {
			Close(context.Context) error
		}); ok {
			if err := closer.Close(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "testagent: hook Close: %v\n", err)
			}
		}
		if err := d.MCP.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: mcp Close: %v\n", err)
		}
	}

	if isatty.IsTerminal(os.Stdin.Fd()) {
		// TUI path: bubbletea handles SIGWINCH (WindowSizeMsg) and rendering.
		// Wire SIGINT/SIGTERM into a one-shot channel that runTUI forwards to
		// the program's Quit().
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		quitCh := make(chan struct{})
		go func() {
			<-sigCh
			close(quitCh)
		}()

		code, reason := runTUI(ctx, g, d, quitCh)
		if reason == "" {
			reason = "other"
		}
		shutdown(reason)
		return code
	}

	// Scanner path: piped stdin (e.g. e2e tests) — keeps the deterministic
	// inline rendering that the e2e regex assertions rely on.
	runScanner(ctx, g, d, shutdown)
	return 0
}
