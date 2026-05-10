package engine

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/slash"
)

// TestMain primes the runtime's signal-handling goroutine BEFORE any
// synctest bubble runs. The first signal.Notify call lazily spawns
// runtime.ensureSigM, which captures the channels it creates into the
// current synctest scope. Doing it here, outside any bubble, ensures the
// runtime goroutine selects on un-bubbled channels — otherwise a later
// signal.Notify call from inside runScanner (under synctest.Test) panics
// with "select on synctest channel from outside bubble".
func TestMain(m *testing.M) {
	priming := make(chan os.Signal, 1)
	signal.Notify(priming, syscall.SIGUSR1)
	signal.Stop(priming)
	os.Exit(m.Run())
}

// scannerTestDeps builds the minimum viable Deps for runScanner. The
// hooks sender has nil matchers so OnSessionStart / OnSessionEnd / etc.
// short-circuit without performing HTTP I/O — that I/O would straddle
// the synctest bubble boundary and deadlock the test. The MCP client
// wraps no servers so Connect is a no-op.
func scannerTestDeps() Deps {
	sender := hooks.NewSender(nil, "sid-test", "/tmp", "", "default", nil)
	client := mcp.NewClient(nil)
	handler := slash.New(sender, client, io.Discard)
	return Deps{Hooks: sender, MCP: client, Slash: handler}
}

// TestRunScanner_AutoExit_ReturnsZero verifies the AutoExit goroutine
// signals via channel rather than calling os.Exit, and that runScanner
// returns (0, "other") within bounded virtual time. Stdin is a pipe that
// never closes, so EOF cannot win the race.
func TestRunScanner_AutoExit_ReturnsZero(t *testing.T) {
	t.Parallel()

	d := scannerTestDeps()

	synctest.Test(t, func(t *testing.T) {

		// Pipe with no writer-close keeps the inputCh reader goroutine
		// durably blocked on Read, so AutoExit is the only path that can
		// unblock the main select. We close pw AFTER runScanner returns
		// so the reader goroutine exits and the synctest bubble drains.
		pr, pw := io.Pipe()

		g := Globals{
			Name:      "Test",
			SessionID: "sid-test",
			AutoExit:  50 * time.Millisecond,
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		type result struct {
			code   int
			reason string
		}
		done := make(chan result, 1)
		go func() {
			code, reason := runScanner(ctx, g, d, pr)
			done <- result{code, reason}
		}()

		start := time.Now()
		got := <-done
		elapsed := time.Since(start)
		// Unblock the inputCh reader goroutine so the bubble drains.
		_ = pw.Close()

		if got.code != 0 {
			t.Errorf("code = %d, want 0", got.code)
		}
		if got.reason != "other" {
			t.Errorf("reason = %q, want %q", got.reason, "other")
		}
		// Auto-exit should fire at exactly AutoExit virtual time.
		if elapsed < 50*time.Millisecond {
			t.Errorf("returned before AutoExit elapsed: %s", elapsed)
		}
		if elapsed > 200*time.Millisecond {
			t.Errorf("returned far past AutoExit (%s); should be ~50ms", elapsed)
		}
	})
}

// TestRunScanner_ExitWins verifies that a /exit slash command's non-zero
// code is returned BEFORE AutoExit could have fired. Pre-PR, the AutoExit
// goroutine's hardcoded os.Exit(0) could race with /exit's exit code; the
// fix routes both through the main loop's return.
func TestRunScanner_ExitWins(t *testing.T) {
	t.Parallel()

	d := scannerTestDeps()

	synctest.Test(t, func(t *testing.T) {

		// Stdin contains exactly one line: a /exit 7 command. After the
		// reader emits the line, it sees EOF on the strings.Reader; the
		// main loop processes /exit before consuming any further input.
		stdin := strings.NewReader("/exit 7\n")

		g := Globals{
			Name:      "Test",
			SessionID: "sid-test",
			AutoExit:  10 * time.Second, // far beyond the /exit dispatch
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		type result struct {
			code   int
			reason string
		}
		done := make(chan result, 1)
		go func() {
			code, reason := runScanner(ctx, g, d, stdin)
			done <- result{code, reason}
		}()

		start := time.Now()
		got := <-done
		elapsed := time.Since(start)

		if got.code != 7 {
			t.Errorf("code = %d, want 7", got.code)
		}
		if got.reason != "logout" {
			t.Errorf("reason = %q, want %q", got.reason, "logout")
		}
		// /exit must resolve well before AutoExit (10s) — the slash
		// dispatch and return path are synchronous, so virtual time
		// should not advance past the slash dispatch itself.
		if elapsed >= 10*time.Second {
			t.Errorf("AutoExit fired before /exit; elapsed=%s", elapsed)
		}
	})
}
