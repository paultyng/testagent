// Package claude implements testagent's "claude" subcommand — the v1
// drop-in fake for Claude Code. Vendor-specific flags, on-disk schema
// types (Settings, MCPConfig), and the --print/-p output formatter all
// live here.
package claude

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/engine"
	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/rootflags"
	"github.com/paultyng/testagent/internal/slash"
)

// stringSlice implements pflag.Value for repeatable string flags (--add-dir).
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }
func (s *stringSlice) Type() string       { return "stringSlice" }

// NewCommand returns the "claude" subcommand wired against the given root
// flags. RunE constructs hooks/mcp/slash deps and dispatches to either
// runPrint (--print/-p) or engine.Run.
func NewCommand(rf *rootflags.Flags) *cobra.Command {
	var (
		addDirs      stringSlice
		name         string
		printMode    bool
		sessionID    string
		resume       string
		settingsPath string
		mcpPath      string
		systemPrompt string
		outputFormat string
	)

	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Emulate the Claude Code CLI",
		Long: `Drives an interactive Claude Code-shaped session: argv compatibility,
HTTP hooks (UserPromptSubmit / PostToolUse / Stop / SessionStart / SessionEnd),
MCP HTTP client, and --print/--output-format stream-json.

Bare invocation (testagent <flags>) defaults to this subcommand.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := loadSettings(settingsPath)
			if err != nil {
				return err
			}
			mcpConfig, err := loadMCPConfig(mcpPath)
			if err != nil {
				return err
			}

			// Resolve session identity. --resume wins; otherwise --session-id; otherwise generate.
			sid := sessionID
			if resume != "" {
				sid = resume
			}
			if sid == "" {
				sid = newSessionID()
			}
			cwd, _ := os.Getwd()
			transcriptPath := fmt.Sprintf("/tmp/testagent-transcript-%s.jsonl", sid)
			const permissionMode = "default"

			var debugW io.Writer
			if rf.Verbose {
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
			slashHandler := slash.New(hookSender, mcpClient, os.Stdout)

			// Non-interactive mode (--print / -p): one-shot, exit when done.
			if printMode {
				ctx := ctxOrBackground(cmd)
				if err := mcpClient.Connect(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "testagent: mcp Connect: %v\n", err)
				}
				code := runPrint(ctx, printOptions{
					name:         name,
					sessionID:    sid,
					cwd:          cwd,
					outputFormat: outputFormat,
					positional:   args,
					resumed:      resume != "",
					hooks:        hookSender,
					mcp:          mcpClient,
				}, os.Stdin, os.Stdout)
				if code != 0 {
					return &ExitError{Code: code}
				}
				return nil
			}

			statusLine := loadedStatus(settings, mcpConfig, systemPrompt, addDirs)

			g := engine.Globals{
				Emulator:    "Claude",
				Name:        name,
				SessionID:   sid,
				Resumed:     resume != "",
				ThinkDelay:  rf.ThinkDelay,
				StreamDelay: rf.StreamDelay,
				ExitAfter:   rf.ExitAfter,
				AutoExit:    rf.AutoExit,
				HistoryCap:  rf.HistoryCap,
				StatusLine:  statusLine,
			}
			d := engine.Deps{
				Hooks: hookSender,
				MCP:   mcpClient,
				Slash: slashHandler,
			}
			if code := engine.Run(ctxOrBackground(cmd), g, d); code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&sessionID, "session-id", "", "session ID (new session)")
	f.StringVar(&resume, "resume", "", "session ID to resume")
	f.StringVar(&settingsPath, "settings", "", "path to Claude Code settings.json (hooks)")
	f.StringVar(&mcpPath, "mcp-config", "", "path to Claude Code MCP config JSON")
	f.StringVar(&systemPrompt, "append-system-prompt", "", "system prompt text to append")
	f.StringVar(&outputFormat, "output-format", "text", "output format: text|json|stream-json (used with --print)")
	f.StringVarP(&name, "name", "n", "test-agent", "session name for the banner")
	f.BoolVarP(&printMode, "print", "p", false, "non-interactive mode (one-shot)")
	f.Var(&addDirs, "add-dir", "additional directory (repeatable)")
	return cmd
}

// ctxOrBackground returns cmd.Context() if set, else context.Background().
// Cobra threads context through Execute; subcommand RunE gets the inherited
// one when callers used ExecuteContext.
func ctxOrBackground(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}
