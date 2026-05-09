// testagent is a mock terminal agent for UI and integration testing.
// It emulates an interactive Claude Code session without calling any LLM APIs.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/paultyng/testagent/internal/engine"
	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/slash"
)

// Settings mirrors Claude Code's settings.json shape (hooks + permissions).
type Settings struct {
	Hooks       map[string][]hooks.Matcher `json:"hooks,omitempty"`
	Permissions *Permissions               `json:"permissions,omitempty"`
}

type Permissions struct {
	Allow []string `json:"allow,omitempty"`
}

// MCPConfig mirrors Claude Code's --mcp-config file shape.
type MCPConfig struct {
	MCPServers map[string]mcp.Server `json:"mcpServers"`
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
	var matchers map[string][]hooks.Matcher
	if settings != nil {
		matchers = settings.Hooks
	}
	hookSender := hooks.NewSender(matchers, sid, cwd, transcriptPath, permissionMode, debugW)
	var mcpServers map[string]mcp.Server
	if mcpConfig != nil {
		mcpServers = mcpConfig.MCPServers
	}
	mcpClient := mcp.NewClient(mcpServers)
	slashHandler := slash.New(30*time.Millisecond, hookSender, mcpClient, os.Stdout)

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
			hooks:        hookSender,
			mcp:          mcpClient,
		}, os.Stdin, os.Stdout))
	}

	statusLine := loadedStatus(settings, mcpConfig, *systemPrompt, addDirs)

	g := engine.Globals{
		Name:       name,
		SessionID:  sid,
		Resumed:    *resume != "",
		Delay:      *delay,
		ExitAfter:  *exitAfter,
		AutoExit:   *autoExit,
		HistoryCap: *historyCap,
		StatusLine: statusLine,
	}
	d := engine.Deps{
		Hooks: hookSender,
		MCP:   mcpClient,
		Slash: slashHandler,
	}
	os.Exit(engine.Run(context.Background(), g, d))
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
