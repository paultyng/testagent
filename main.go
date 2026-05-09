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

func main() {
	var (
		addDirs      stringSlice
		name         string
		printMode    bool
		delay        = flag.Duration("delay", 200*time.Millisecond, "simulated thinking delay before response")
		exitAfter    = flag.Int("exit-after", 0, "auto-exit after N interactions (0 = never)")
		autoExit     = flag.Duration("auto-exit", 0, "auto-exit after duration (0 = disabled)")
		sessionID    = flag.String("session-id", "", "session ID (new session)")
		resume       = flag.String("resume", "", "session ID to resume")
		settingsPath = flag.String("settings", "", "path to Claude Code settings.json (hooks)")
		mcpPath      = flag.String("mcp-config", "", "path to Claude Code MCP config JSON")
		systemPrompt = flag.String("append-system-prompt", "", "system prompt text to append")
		outputFormat = flag.String("output-format", "text", "output format: text|json|stream-json")
	)
	flag.StringVar(&name, "name", "test-agent", "session name for the banner")
	flag.StringVar(&name, "n", "test-agent", "session name for the banner (short)")
	flag.BoolVar(&printMode, "print", false, "non-interactive mode (one-shot)")
	flag.BoolVar(&printMode, "p", false, "non-interactive mode (short)")
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

	hooks := NewHookSender(settings, sid, cwd, transcriptPath, permissionMode)
	mcpClient := NewMCPClient(mcpConfig)
	shutdown := func(reason string) {
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

	// Handle SIGTERM gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Handle SIGWINCH (terminal resize).
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)

	// Banner via lipgloss: rounded border auto-sizes to widest line and
	// handles wide / multi-byte characters correctly (the prior hand-rolled
	// pad miscounted UTF-8 bytes when the session id was truncated with an
	// ellipsis).
	sessionLabel := "session"
	if *resume != "" {
		sessionLabel = "resumed"
	}
	bannerContent := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11")).Render(name),
		lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Faint(true).Render(sessionLabel+" "+sid),
		lipgloss.NewStyle().Faint(true).Render("Type anything; /help for commands"),
	)
	fmt.Println(lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("14")).
		Padding(0, 2).
		Render(bannerContent))

	if status := loadedStatus(settings, mcpConfig, *systemPrompt, addDirs); status != "" {
		fmt.Printf("\033[2m[%s]\033[0m\n", status)
	}

	// Connect to MCP servers (best-effort; logged on failure, session continues).
	if err := mcpClient.Connect(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "testagent: mcp Connect: %v\n", err)
	} else if tools := mcpClient.Tools(); len(tools) > 0 {
		fmt.Printf("\033[2m[mcp connected: %d tools]\033[0m\n", len(tools))
	}

	fmt.Printf("\033[32m>\033[0m ")

	// Auto-exit after a duration (for headless tests where no input is sent).
	if *autoExit > 0 {
		go func() {
			time.Sleep(*autoExit)
			fmt.Printf("\n\033[2m[auto-exit after %s]\033[0m\n", *autoExit)
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

		// Slash commands drive UI primitives (tool blocks, panels, MCP calls,
		// etc.) without going through the echo path. They don't fire OnPrompt.
		if outcome := slash.Dispatch(context.Background(), input); outcome.Handled {
			if outcome.Exit {
				shutdown(outcome.Reason)
				os.Exit(outcome.ExitCode)
			}
			fmt.Printf("\033[32m>\033[0m ")
			continue
		}

		if err := hooks.OnPrompt(context.Background(), input, name); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnPrompt: %v\n", err)
		}

		count++

		// Simulate thinking with a spinner + elapsed-seconds counter
		// (matches the visual shape of real Claude's "thinking…" state).
		showThinking(os.Stdout, *delay)

		// Echo response with color.
		fmt.Printf("\033[1;35m[%s]\033[0m %s\n", name, input)
		fmt.Printf("\033[32m>\033[0m ")
		lastAssistant = fmt.Sprintf("[%s] %s", name, input)

		if err := hooks.OnStop(context.Background(), lastAssistant, false); err != nil {
			fmt.Fprintf(os.Stderr, "testagent: hook OnStop: %v\n", err)
		}

		if *exitAfter > 0 && count >= *exitAfter {
			fmt.Printf("\n\033[2m[exit-after %d reached]\033[0m\n", *exitAfter)
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
func showThinking(out io.Writer, total time.Duration) {
	if total < 200*time.Millisecond {
		time.Sleep(total)
		return
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	start := time.Now()
	deadline := start.Add(total)
	const tick = 100 * time.Millisecond

	// Print a blank line first so we have a row to repaint and a row below
	// it where the cursor can rest.
	fmt.Fprintln(out)

	for i := 0; ; i++ {
		elapsed := time.Since(start).Truncate(time.Second)
		// Move cursor up to the spinner row, clear it, write the new content,
		// and newline so the cursor returns to the row below.
		fmt.Fprintf(out, "\033[1A\033[2K\033[2m%s thinking… (%s · esc to interrupt)\033[0m\n",
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
	// Clear the spinner row; cursor ends at column 0 of that row, ready for
	// the next print.
	fmt.Fprint(out, "\033[1A\033[2K\r")
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
