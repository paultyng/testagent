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
	Emulator    string        // "Claude", "Codex", etc. — vendor type prefix shown in the banner
	Name        string        // user-supplied session label (--name)
	SessionID   string
	Resumed     bool
	Delay       time.Duration // default thinking-spinner duration per turn
	StreamDelay time.Duration // default per-token interval for the response stream
	ExitAfter   int
	AutoExit    time.Duration
	HistoryCap  int    // 0 = unlimited
	StatusLine  string // shown under the banner; empty = omitted
}

// Deps are the runtime dependencies the engine drives. All fields are
// required.
type Deps struct {
	Hooks *hooks.Sender
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
