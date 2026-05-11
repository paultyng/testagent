// Scanner-based fallback loop for piped stdin. Used when stdin is not a
// TTY (e2e tests, automation pipelines) — keeps the deterministic inline
// rendering that those callers' regex assertions rely on.

package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/paultyng/testagent/internal/render"
)

// runScanner is the bufio.Scanner-driven interactive loop. It returns
// (exit code, shutdown reason) so the caller can run the shutdown
// closure once, in one place, with the right reason — mirroring runTUI's
// shape. Returning instead of calling os.Exit inside the loop closes the
// race where an AutoExit goroutine could fire os.Exit(0) and silently
// override the non-zero exit code from a /exit slash command.
func runScanner(ctx context.Context, g Globals, d Deps, stdin io.Reader) (int, string) {
	// Register signal handlers BEFORE any potentially-blocking I/O (banner
	// render is fast, but MCP Connect can hang on an unreachable server).
	// SIGINT/SIGTERM during connect must still trigger graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Handle terminal resize. Unix delivers SIGWINCH; Windows has no
	// equivalent signal, so notifyResize is a no-op there and the
	// resize-echo goroutine below sees an idle channel.
	winchCh := make(chan os.Signal, 1)
	notifyResize(winchCh)

	// Banner via lipgloss: rounded border auto-sizes to widest line and
	// handles wide / multi-byte characters correctly.
	sessionLabel := "session"
	if g.Resumed {
		sessionLabel = "resumed"
	}
	bannerContent := lipgloss.JoinVertical(lipgloss.Left,
		bannerTitle(g.Emulator, g.Name),
		render.BannerMetaStyle.Faint(true).Render(sessionLabel+" "+g.SessionID),
		render.MuteStyle.Render("Type anything; /help for commands"),
	)
	fmt.Println(render.BannerStyle.Render(bannerContent))

	if g.StatusLine != "" {
		fmt.Println(render.Lifecycle(g.StatusLine))
	}

	// Connect to MCP servers (best-effort; logged on failure, session continues).
	if err := d.MCP.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: mcp Connect: %v\n", err)
	} else if tools := d.MCP.Tools(); len(tools) > 0 {
		fmt.Println(render.Lifecycle(fmt.Sprintf("mcp connected: %d tools", len(tools))))
	}

	// SessionStart fires after MCP is up so orchestrators see a complete boot
	// state. source mirrors Claude Code's vocabulary: "resume" iff the caller
	// passed --resume, "startup" otherwise.
	startSource := "startup"
	if g.Resumed {
		startSource = "resume"
	}
	if err := d.Hooks.OnSessionStart(ctx, startSource); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnSessionStart: %v\n", err)
	}

	fmt.Print(render.Prompt())

	// loopDone is closed when the main loop returns. The auxiliary
	// goroutines (auto-exit timer, resize handler, stdin reader) watch
	// this so they unblock and exit instead of leaking — this matters
	// for the synctest tests in scanner_test.go, which deadlock if any
	// bubble goroutine is still running after the test goroutine exits.
	// In production runScanner returning means the process is about to
	// exit, so leaks would be cosmetic; the cleanup is for testability.
	loopDone := make(chan struct{})
	defer close(loopDone)

	// Auto-exit after a duration (for headless tests where no input is sent).
	// The goroutine signals via autoExitCh; the main loop owns the return
	// (and therefore os.Exit, in Run). Buffered so the goroutine never
	// blocks if the loop exits via another path first.
	autoExitCh := make(chan struct{}, 1)
	if g.AutoExit > 0 {
		go func() {
			t := time.NewTimer(g.AutoExit)
			defer t.Stop()
			select {
			case <-t.C:
				select {
				case autoExitCh <- struct{}{}:
				default:
				}
			case <-loopDone:
			}
		}()
	}

	// Process resize events in background until the loop returns.
	go func() {
		for {
			select {
			case <-loopDone:
				return
			case _, ok := <-winchCh:
				if !ok {
					return
				}
				rows, cols := getTermSize()
				fmt.Printf("\n%s\n%s", render.Lifecycle(fmt.Sprintf("resized: %dx%d", cols, rows)), render.Prompt())
			}
		}
	}()

	// inputCh decouples the blocking bufio.Scan from the select loop so
	// signals / auto-exit / context cancel can preempt a stuck read. The
	// channel carries each scanned line; closing it signals EOF. The
	// reader exits naturally on EOF; if the main loop returns first
	// (e.g. /exit), the goroutine is left blocked on Read until stdin
	// closes — acceptable in production (process is about to exit) and
	// the synctest tests in scanner_test.go close their pipe writer to
	// unblock it.
	inputCh := make(chan string)
	go func() {
		defer close(inputCh)
		scanner := bufio.NewScanner(stdin)
		for scanner.Scan() {
			select {
			case inputCh <- scanner.Text():
			case <-loopDone:
				return
			}
		}
	}()

	count := 0
	var lastAssistant string

	for {
		var input string
		var ok bool
		select {
		case <-ctx.Done():
			fmt.Printf("\n%s\n", render.ErrorStyle.Render("Goodbye!"))
			return 0, "other"
		case <-sigCh:
			fmt.Printf("\n%s\n", render.ErrorStyle.Render("Goodbye!"))
			return 0, "other"
		case <-autoExitCh:
			fmt.Printf("\n%s\n", render.Lifecycle(fmt.Sprintf("auto-exit after %s", g.AutoExit)))
			return 0, "other"
		case input, ok = <-inputCh:
			if !ok {
				// EOF on stdin.
				return 0, "other"
			}
		}

		input = strings.TrimSpace(input)
		if input == "" {
			fmt.Print(render.Prompt())
			continue
		}

		if input == "exit" || input == "quit" {
			fmt.Println(render.ErrorStyle.Render("Goodbye!"))
			return 0, "logout"
		}

		// Slash commands drive UI primitives (fake-tool blocks, panels, MCP
		// calls, etc.) without going through the echo path. /think and
		// /stream are duration overrides — they signal via outcome.Prompt
		// that the message should run through the same prompt path as raw
		// input.
		promptLine := input
		thinkDur := g.ThinkDelay
		streamDur := g.StreamDelay
		if outcome := d.Slash.Dispatch(ctx, input); outcome.Handled {
			if outcome.Exit {
				return outcome.ExitCode, outcome.Reason
			}
			if outcome.Restart {
				// Simulate a Claude /clear or /compact reset on the wire:
				// flush any pending /fake-tool so its PostToolUse fires
				// before SessionEnd (same invariant the shutdown closure
				// documents). For /compact and /fake-auto-compact, the
				// SessionEnd/SessionStart pair is bracketed by PreCompact
				// and PostCompact carrying outcome.CompactTrigger. No
				// scrollback wipe — that's a future UI primitive.
				d.Slash.FlushPendingTool(ctx)
				if outcome.CompactTrigger != "" {
					if err := d.Hooks.OnPreCompact(ctx, outcome.CompactTrigger); err != nil {
						fmt.Fprintf(os.Stderr, "testagent: hook OnPreCompact: %v\n", err)
					}
				}
				if err := d.Hooks.OnSessionEnd(ctx, outcome.RestartReason); err != nil {
					fmt.Fprintf(os.Stderr, "testagent: hook OnSessionEnd: %v\n", err)
				}
				if err := d.Hooks.OnSessionStart(ctx, outcome.RestartReason); err != nil {
					fmt.Fprintf(os.Stderr, "testagent: hook OnSessionStart: %v\n", err)
				}
				if outcome.CompactTrigger != "" {
					if err := d.Hooks.OnPostCompact(ctx, outcome.CompactTrigger); err != nil {
						fmt.Fprintf(os.Stderr, "testagent: hook OnPostCompact: %v\n", err)
					}
				}
				fmt.Print(render.Prompt())
				continue
			}
			if outcome.Prompt == "" {
				// Pure slash side-effect (panel, fake-tool, mcp-call, etc.).
				fmt.Print(render.Prompt())
				continue
			}
			// /think or /stream — route the message through the regular
			// prompt path with one of the durations overridden.
			promptLine = outcome.Prompt
			if outcome.HasThinkDuration {
				thinkDur = outcome.ThinkDuration
			}
			if outcome.HasStreamDuration {
				streamDur = outcome.StreamDuration
			}
		}

		if err := d.Hooks.OnPrompt(ctx, promptLine, g.Name); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnPrompt: %v\n", err)
		}

		count++

		// Simulate thinking with a spinner + elapsed-seconds counter
		// (matches the visual shape of real Claude's "thinking…" state).
		showThinking(os.Stdout, thinkDur)

		// Stream the echo response token-by-token at streamDur per token.
		// The assembled bytes match the prior single-Println shape, just
		// paced — so e2e regex assertions over the full echo string keep
		// matching.
		streamEcho(os.Stdout, g.Name, promptLine, streamDur)
		fmt.Print(render.Prompt())
		lastAssistant = fmt.Sprintf("[%s] %s", g.Name, promptLine)

		if err := d.Hooks.OnStop(ctx, lastAssistant, false); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnStop: %v\n", err)
		}

		if g.ExitAfter > 0 && count >= g.ExitAfter {
			fmt.Printf("\n%s\n", render.Lifecycle(fmt.Sprintf("exit-after %d reached", g.ExitAfter)))
			return 0, "other"
		}
	}
}
