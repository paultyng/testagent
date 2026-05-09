// testagent is a mock terminal agent for UI and integration testing.
// It emulates an interactive Claude Code session without calling any LLM APIs.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

// Settings mirrors Claude Code's settings.json shape (hooks + permissions).
type Settings struct {
	Hooks       map[string][]HookMatcher `json:"hooks,omitempty"`
	Permissions *Permissions             `json:"permissions,omitempty"`
}

type HookMatcher struct {
	Matcher string `json:"matcher"`
	Hooks   []Hook `json:"hooks"`
}

type Hook struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Timeout int               `json:"timeout"`
	Headers map[string]string `json:"headers,omitempty"`
}

type Permissions struct {
	Allow []string `json:"allow,omitempty"`
}

// MCPConfig mirrors Claude Code's --mcp-config file shape.
type MCPConfig struct {
	MCPServers map[string]MCPServer `json:"mcpServers"`
}

type MCPServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// stringSlice implements flag.Value for repeatable string flags (--add-dir).
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

// scannerOptions bundles the inputs runScannerLoop needs from main().
type scannerOptions struct {
	name           string
	sessionID      string
	resumed        bool
	cwd            string
	transcriptPath string
	permissionMode string
	delay          time.Duration
	exitAfter      int
	autoExit       time.Duration
	statusLine     string
	hooks          *HookSender
	mcp            *MCPClient
	slash          *SlashHandler
}

func main() {
	var (
		addDirs      stringSlice
		name         string
		printMode    bool
		verbose      bool
		delay        = flag.Duration("delay", 3*time.Second, "simulated thinking delay before response (also the default for /think)")
		exitAfter    = flag.Int("exit-after", 0, "auto-exit after N interactions (0 = never)")
		autoExit     = flag.Duration("auto-exit", 0, "auto-exit after duration (0 = disabled)")
		sessionID    = flag.String("session-id", "", "session ID (new session)")
		resume       = flag.String("resume", "", "session ID to resume")
		settingsPath = flag.String("settings", "", "path to Claude Code settings.json (hooks)")
		mcpPath      = flag.String("mcp-config", "", "path to Claude Code MCP config JSON")
		systemPrompt = flag.String("append-system-prompt", "", "system prompt text to append")
		outputFormat = flag.String("output-format", "text", "output format: text|json|stream-json")
		historyCap   = flag.Int("history-cap", 1000, "TUI history cap (0 = unlimited)")
	)
	flag.StringVar(&name, "name", "test-agent", "session name for the banner")
	flag.StringVar(&name, "n", "test-agent", "session name for the banner (short)")
	flag.BoolVar(&printMode, "print", false, "non-interactive mode (one-shot)")
	flag.BoolVar(&printMode, "p", false, "non-interactive mode (short)")
	flag.BoolVar(&verbose, "verbose", false, "log hook activity to stderr")
	flag.BoolVar(&verbose, "v", false, "log hook activity to stderr (short)")
	flag.Var(&addDirs, "add-dir", "additional directory (repeatable)")
	flag.Parse()

	settings, err := loadSettings(*settingsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testagent: %v\n", err)
		os.Exit(1)
	}
	mcpConfig, err := loadMCPConfig(*mcpPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testagent: %v\n", err)
		os.Exit(1)
	}

	// Resolve session identity. --resume wins; otherwise --session-id; otherwise generate.
	sid := *sessionID
	if *resume != "" {
		sid = *resume
	}
	if sid == "" {
		sid = newSessionID()
	}
	cwd, _ := os.Getwd()
	transcriptPath := fmt.Sprintf("/tmp/testagent-transcript-%s.jsonl", sid)
	const permissionMode = "default"

	var debugW io.Writer
	if verbose {
		debugW = os.Stderr
	}
	hooks := NewHookSender(settings, sid, cwd, transcriptPath, permissionMode, debugW)
	mcpClient := NewMCPClient(mcpConfig)
	slash := &SlashHandler{
		name:           name,
		streamDelay:    30 * time.Millisecond,
		sessionID:      sid,
		cwd:            cwd,
		transcriptPath: transcriptPath,
		permissionMode: permissionMode,
		hooks:          hooks,
		mcp:            mcpClient,
		out:            os.Stdout,
	}
	shutdown := func(reason string) {
		// Flush any in-flight /fake-tool that never got a /fake-tool-result so its
		// PostToolUse fires (with empty response) before SessionEnd.
		slash.FlushPendingTool(context.Background())
		if err := hooks.OnSessionEnd(context.Background(), reason); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnSessionEnd: %v\n", err)
		}
		if err := mcpClient.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: mcp Close: %v\n", err)
		}
	}

	// Non-interactive mode (--print / -p): one-shot, exit when done.
	if printMode {
		// Connect MCP so init events / tool listings reflect reality, but
		// failures don't abort: real claude continues without MCP if down.
		if err := mcpClient.Connect(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: mcp Connect: %v\n", err)
		}
		os.Exit(runPrint(context.Background(), printOptions{
			name:         name,
			sessionID:    sid,
			cwd:          cwd,
			outputFormat: *outputFormat,
			positional:   flag.Args(),
			hooks:        hooks,
			mcp:          mcpClient,
		}, os.Stdin, os.Stdout))
	}

	statusLine := loadedStatus(settings, mcpConfig, *systemPrompt, addDirs)
	interactive := isatty.IsTerminal(os.Stdin.Fd())

	if interactive {
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

		code, reason := runTUI(context.Background(), tuiOptions{
			name:           name,
			sessionID:      sid,
			resumed:        *resume != "",
			cwd:            cwd,
			transcriptPath: transcriptPath,
			permissionMode: permissionMode,
			delay:          *delay,
			exitAfter:      *exitAfter,
			autoExit:       *autoExit,
			historyCap:     *historyCap,
			statusLine:     statusLine,
			settings:       settings,
			mcpConfig:      mcpConfig,
			hooks:          hooks,
			mcp:            mcpClient,
			slash:          slash,
		}, quitCh)
		if reason == "" {
			reason = "other"
		}
		shutdown(reason)
		os.Exit(code)
	}

	// Scanner path: piped stdin (e.g. e2e tests) — keeps the deterministic
	// inline rendering that the e2e regex assertions rely on.
	runScannerLoop(context.Background(), scannerOptions{
		name:           name,
		sessionID:      sid,
		resumed:        *resume != "",
		cwd:            cwd,
		transcriptPath: transcriptPath,
		permissionMode: permissionMode,
		delay:          *delay,
		exitAfter:      *exitAfter,
		autoExit:       *autoExit,
		statusLine:     statusLine,
		hooks:          hooks,
		slash:          slash,
		mcp:            mcpClient,
	}, shutdown)
}

// runScannerLoop is the original bufio.Scanner-driven interactive loop.
// Used when stdin is not a TTY (piped input). The shutdown closure fires
// SessionEnd + closes MCP and is invoked here on /exit, EOF, or signal.
func runScannerLoop(ctx context.Context, opts scannerOptions, shutdown func(string)) {
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
	if opts.resumed {
		sessionLabel = "resumed"
	}
	bannerContent := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render(opts.name),
		lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Faint(true).Render(sessionLabel+" "+opts.sessionID),
		lipgloss.NewStyle().Faint(true).Render("Type anything; /help for commands"),
	)
	fmt.Println(lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("14")).
		Padding(0, 2).
		Render(bannerContent))

	if opts.statusLine != "" {
		fmt.Printf("\033[2m[%s]\033[0m\n", opts.statusLine)
	}

	// Connect to MCP servers (best-effort; logged on failure, session continues).
	if err := opts.mcp.Connect(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: mcp Connect: %v\n", err)
	} else if tools := opts.mcp.Tools(); len(tools) > 0 {
		fmt.Printf("\033[2m[mcp connected: %d tools]\033[0m\n", len(tools))
	}

	// SessionStart fires after MCP is up so orchestrators see a complete boot
	// state. source mirrors Claude Code's vocabulary: "resume" iff the caller
	// passed --resume, "startup" otherwise.
	startSource := "startup"
	if opts.resumed {
		startSource = "resume"
	}
	if err := opts.hooks.OnSessionStart(ctx, startSource); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: hook OnSessionStart: %v\n", err)
	}

	fmt.Printf("\033[32m>\033[0m ")

	// Auto-exit after a duration (for headless tests where no input is sent).
	if opts.autoExit > 0 {
		go func() {
			time.Sleep(opts.autoExit)
			fmt.Printf("\n\033[2m[auto-exit after %s]\033[0m\n", opts.autoExit)
			shutdown("other")
			os.Exit(0)
		}()
	}

	// Process resize events in background.
	go func() {
		for range winchCh {
			rows, cols := getTermSize()
			fmt.Printf("\n\033[2m[resized: %dx%d]\033[0m\n\033[32m>\033[0m ", cols, rows)
		}
	}()

	slash := opts.slash

	scanner := bufio.NewScanner(os.Stdin)
	count := 0
	var lastAssistant string

	for {
		select {
		case <-sigCh:
			fmt.Printf("\n\033[1;31mGoodbye!\033[0m\n")
			shutdown("other")
			os.Exit(0)
		default:
		}

		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			fmt.Printf("\033[32m>\033[0m ")
			continue
		}

		if input == "exit" || input == "quit" {
			fmt.Printf("\033[1;31mGoodbye!\033[0m\n")
			shutdown("logout")
			return
		}

		// Slash commands drive UI primitives (fake-tool blocks, panels, MCP
		// calls, etc.) without going through the echo path. They don't fire
		// OnPrompt — except /think, which signals via outcome.Prompt that it
		// should run through the same prompt path as raw input.
		promptLine := input
		thinkDur := opts.delay
		if outcome := slash.Dispatch(ctx, input); outcome.Handled {
			if outcome.Exit {
				shutdown(outcome.Reason)
				os.Exit(outcome.ExitCode)
			}
			if outcome.Restart {
				// Simulate a Claude /clear or /compact reset on the wire:
				// SessionEnd then SessionStart with the same matcher value.
				// The process keeps running — no scrollback wipe (that's a
				// future UI feature; this PR is hook-shape only).
				if err := opts.hooks.OnSessionEnd(ctx, outcome.RestartReason); err != nil {
					fmt.Fprintf(os.Stderr, "testagent: hook OnSessionEnd: %v\n", err)
				}
				if err := opts.hooks.OnSessionStart(ctx, outcome.RestartReason); err != nil {
					fmt.Fprintf(os.Stderr, "testagent: hook OnSessionStart: %v\n", err)
				}
				fmt.Printf("\033[32m>\033[0m ")
				continue
			}
			if outcome.Prompt == "" && !outcome.HasThinkDuration {
				// Pure slash side-effect (panel, fake-tool, mcp-call, etc.).
				fmt.Printf("\033[32m>\033[0m ")
				continue
			}
			// /think — route the message through the regular prompt path.
			promptLine = outcome.Prompt
			if outcome.HasThinkDuration {
				thinkDur = outcome.ThinkDuration
			}
		}

		if err := opts.hooks.OnPrompt(ctx, promptLine, opts.name); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnPrompt: %v\n", err)
		}

		count++

		// Simulate thinking with a spinner + elapsed-seconds counter
		// (matches the visual shape of real Claude's "thinking…" state).
		showThinking(os.Stdout, thinkDur)

		// Echo response with color.
		fmt.Printf("\033[1;35m[%s]\033[0m %s\n", opts.name, promptLine)
		fmt.Printf("\033[32m>\033[0m ")
		lastAssistant = fmt.Sprintf("[%s] %s", opts.name, promptLine)

		if err := opts.hooks.OnStop(ctx, lastAssistant, false); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnStop: %v\n", err)
		}

		if opts.exitAfter > 0 && count >= opts.exitAfter {
			fmt.Printf("\n\033[2m[exit-after %d reached]\033[0m\n", opts.exitAfter)
			shutdown("other")
			return
		}
	}

	shutdown("other")
}

// newSessionID generates a UUID-v4-shaped session identifier.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}

// loadSettings parses a Claude Code settings.json file. Empty path returns nil.
func loadSettings(path string) (*Settings, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading settings %s: %w", path, err)
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parsing settings %s: %w", path, err)
	}
	return &s, nil
}

// loadMCPConfig parses a Claude Code --mcp-config file. Empty path returns nil.
func loadMCPConfig(path string) (*MCPConfig, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading mcp config %s: %w", path, err)
	}
	var c MCPConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parsing mcp config %s: %w", path, err)
	}
	return &c, nil
}

// loadedStatus returns a one-line summary of what was loaded from flags / config
// files, suitable for display under the banner. Empty when nothing was loaded.
func loadedStatus(s *Settings, m *MCPConfig, systemPrompt string, addDirs []string) string {
	var parts []string
	if s != nil && len(s.Hooks) > 0 {
		names := make([]string, 0, len(s.Hooks))
		for k := range s.Hooks {
			names = append(names, strings.ToLower(k))
		}
		sort.Strings(names)
		parts = append(parts, "hooks: "+strings.Join(names, ", "))
	}
	if m != nil && len(m.MCPServers) > 0 {
		names := make([]string, 0, len(m.MCPServers))
		for k := range m.MCPServers {
			names = append(names, k)
		}
		sort.Strings(names)
		parts = append(parts, "mcp: "+strings.Join(names, ", "))
	}
	if systemPrompt != "" {
		parts = append(parts, fmt.Sprintf("system prompt: %d chars", len(systemPrompt)))
	}
	if len(addDirs) > 0 {
		parts = append(parts, fmt.Sprintf("dirs: %d", len(addDirs)))
	}
	return strings.Join(parts, " | ")
}

// showThinking renders an inline spinner + elapsed-seconds counter for the
// duration of total. The spinner sits on its own line; the cursor rests on
// the line below it, so any output that follows starts cleanly underneath.
// On return the spinner line is cleared and the cursor is at the start of
// that (now-empty) line, ready for the next output. For very short delays
// (< 200ms) it simply sleeps — the animation would be invisible.
// showThinking runs the live "Thinking… (Ns)" spinner for total. On
// completion the spinner row is replaced with a static "Thought for Ns"
// marker (dim italic) that stays in scrollback above whatever the caller
// prints next. total <= 0 returns immediately without animation or marker.
func showThinking(out io.Writer, total time.Duration) {
	if total <= 0 {
		return
	}
	start := time.Now()

	if total >= 200*time.Millisecond {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		deadline := start.Add(total)
		const tick = 100 * time.Millisecond

		// Print a blank line first so we have a row to repaint and a row below
		// it where the cursor can rest.
		fmt.Fprintln(out)

		for i := 0; ; i++ {
			elapsed := time.Since(start).Truncate(time.Second)
			// Move cursor up to the spinner row, clear it, write the new content,
			// and newline so the cursor returns to the row below.
			fmt.Fprintf(out, "\033[1A\033[2K\033[2m%s Thinking… (%s · esc to interrupt)\033[0m\n",
				frames[i%len(frames)], elapsed)
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			if remaining < tick {
				time.Sleep(remaining)
				break
			}
			time.Sleep(tick)
		}
		// Replace the spinner row with the static "Thought for Ns" marker.
		// Cursor ends on the row below, ready for the caller's echo.
		elapsed := time.Since(start).Truncate(time.Second)
		fmt.Fprintf(out, "\033[1A\033[2K\033[2;3mThought for %s\033[0m\n", elapsed)
		return
	}

	// Sub-200ms: skip the animation but still emit the marker so consumers
	// see a consistent shape regardless of how short the thinking phase was.
	time.Sleep(total)
	elapsed := time.Since(start).Truncate(time.Second)
	fmt.Fprintf(out, "\033[2;3mThought for %s\033[0m\n", elapsed)
}

func getTermSize() (rows, cols int) {
	type winsize struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	var ws winsize
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, uintptr(syscall.Stdout),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	return int(ws.Row), int(ws.Col)
}
