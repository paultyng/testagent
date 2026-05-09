// Scanner-based fallback loop for piped stdin. Used when stdin is not a
// TTY (e2e tests, automation pipelines) — keeps the deterministic inline
// rendering that those callers' regex assertions rely on.

package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/paultyng/testagent/internal/render"
)

// runScanner is the bufio.Scanner-driven interactive loop. The shutdown
// closure fires SessionEnd + closes MCP and is invoked here on /exit, EOF,
// or signal.
func runScanner(ctx context.Context, g Globals, d Deps, shutdown func(string)) {
	// Register signal handlers BEFORE any potentially-blocking I/O (banner
	// render is fast, but MCP Connect can hang on an unreachable server).
	// SIGINT/SIGTERM during connect must still trigger graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Handle SIGWINCH (terminal resize).
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)

	// Banner via lipgloss: rounded border auto-sizes to widest line and
	// handles wide / multi-byte characters correctly.
	sessionLabel := "session"
	if g.Resumed {
		sessionLabel = "resumed"
	}
	bannerContent := lipgloss.JoinVertical(lipgloss.Left,
		render.SessionStyle.Render(g.Name),
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

	// Auto-exit after a duration (for headless tests where no input is sent).
	if g.AutoExit > 0 {
		go func() {
			time.Sleep(g.AutoExit)
			fmt.Printf("\n%s\n", render.Lifecycle(fmt.Sprintf("auto-exit after %s", g.AutoExit)))
			shutdown("other")
			os.Exit(0)
		}()
	}

	// Process resize events in background.
	go func() {
		for range winchCh {
			rows, cols := getTermSize()
			fmt.Printf("\n%s\n%s", render.Lifecycle(fmt.Sprintf("resized: %dx%d", cols, rows)), render.Prompt())
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	count := 0
	var lastAssistant string

	for {
		select {
		case <-sigCh:
			fmt.Printf("\n%s\n", render.ErrorStyle.Render("Goodbye!"))
			shutdown("other")
			os.Exit(0)
		default:
		}

		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			fmt.Print(render.Prompt())
			continue
		}

		if input == "exit" || input == "quit" {
			fmt.Println(render.ErrorStyle.Render("Goodbye!"))
			shutdown("logout")
			return
		}

		// Slash commands drive UI primitives (fake-tool blocks, panels, MCP
		// calls, etc.) without going through the echo path. They don't fire
		// OnPrompt — except /think, which signals via outcome.Prompt that it
		// should run through the same prompt path as raw input.
		promptLine := input
		thinkDur := g.Delay
		if outcome := d.Slash.Dispatch(ctx, input); outcome.Handled {
			if outcome.Exit {
				shutdown(outcome.Reason)
				os.Exit(outcome.ExitCode)
			}
			if outcome.Restart {
				// Simulate a Claude /clear or /compact reset on the wire:
				// flush any pending /fake-tool so its PostToolUse fires
				// before SessionEnd (same invariant the shutdown closure
				// documents), then SessionEnd then SessionStart with the
				// same matcher value. The process keeps running — no
				// scrollback wipe (that's a future UI feature).
				d.Slash.FlushPendingTool(ctx)
				if err := d.Hooks.OnSessionEnd(ctx, outcome.RestartReason); err != nil {
					fmt.Fprintf(os.Stderr, "testagent: hook OnSessionEnd: %v\n", err)
				}
				if err := d.Hooks.OnSessionStart(ctx, outcome.RestartReason); err != nil {
					fmt.Fprintf(os.Stderr, "testagent: hook OnSessionStart: %v\n", err)
				}
				fmt.Print(render.Prompt())
				continue
			}
			if outcome.Prompt == "" && !outcome.HasThinkDuration {
				// Pure slash side-effect (panel, fake-tool, mcp-call, etc.).
				fmt.Print(render.Prompt())
				continue
			}
			// /think — route the message through the regular prompt path.
			promptLine = outcome.Prompt
			if outcome.HasThinkDuration {
				thinkDur = outcome.ThinkDuration
			}
		}

		if err := d.Hooks.OnPrompt(ctx, promptLine, g.Name); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnPrompt: %v\n", err)
		}

		count++

		// Simulate thinking with a spinner + elapsed-seconds counter
		// (matches the visual shape of real Claude's "thinking…" state).
		showThinking(os.Stdout, thinkDur)

		// Echo response with color.
		fmt.Println(render.Echo(g.Name, promptLine))
		fmt.Print(render.Prompt())
		lastAssistant = fmt.Sprintf("[%s] %s", g.Name, promptLine)

		if err := d.Hooks.OnStop(ctx, lastAssistant, false); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnStop: %v\n", err)
		}

		if g.ExitAfter > 0 && count >= g.ExitAfter {
			fmt.Printf("\n%s\n", render.Lifecycle(fmt.Sprintf("exit-after %d reached", g.ExitAfter)))
			shutdown("other")
			return
		}
	}

	shutdown("other")
}
