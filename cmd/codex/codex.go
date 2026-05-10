// Package codex implements testagent's "codex" subcommand — the v1 fake
// for OpenAI's Codex CLI. Vendor-specific knobs (codex-shaped flags, the
// `~/.codex/config.toml` loader, AGENTS.md surfacing) live here; the
// shared engine loop in internal/engine drives the actual session.
package codex

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
	"github.com/paultyng/testagent/internal/engine"
	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/slash"
)

// stringSlice implements pflag.Value for repeatable string flags
// (--add-dir, --config). Mirrors cmd/claude's helper.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }
func (s *stringSlice) Type() string       { return "stringSlice" }

// NewCommand returns the "codex" subcommand wired against the given root
// flags. RunE constructs hooks/mcp/slash deps and dispatches to
// engine.Run.
//
// MVP scope (per #13):
//   - Wire the popular subset of codex flags. Flags codex models but
//     testagent doesn't (sandbox, approval policy) are accept-and-ignore.
//   - --cd/-C honored via os.Chdir before any cwd-relative work.
//   - ~/.codex/config.toml loaded if present (skeleton only; commits 2/4
//     consume [mcp_servers] and [hooks]).
//   - AGENTS.md from cwd surfaced in the status line.
//   - No --print equivalent yet (codex's `codex exec` lands in commit 2).
//   - No hooks fired yet (commit 4).
func NewCommand(rf *claude.RootFlags) *cobra.Command {
	var (
		addDirs        stringSlice
		askForApproval string
		cd             string
		configOverride stringSlice
		model          string
		sandbox        string
	)

	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Emulate the Codex CLI",
		Long: `Drives an interactive Codex-shaped session: argv compatibility,
~/.codex/config.toml loading, and AGENTS.md surfacing. Hooks and full
slash-command coverage land in follow-up commits — see #13.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Honor --cd before reading AGENTS.md or anything cwd-relative.
			if cd != "" {
				if err := os.Chdir(cd); err != nil {
					return fmt.Errorf("--cd %s: %w", cd, err)
				}
			}
			cwd, _ := os.Getwd()

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			agentsLine, err := loadAgentsMD(cwd)
			if err != nil {
				return err
			}

			sid := newSessionID()
			transcriptPath := fmt.Sprintf("/tmp/testagent-transcript-%s.jsonl", sid)
			const permissionMode = "default"

			var debugW io.Writer
			if rf.Verbose {
				debugW = os.Stderr
			}
			// Hooks: codex's [hooks] table is shell-command-shaped, not HTTP.
			// MVP wires a no-op HTTP sender so engine.Deps stays satisfied;
			// the real codex hook runner lands in commit 4.
			hookSender := hooks.NewSender(nil, sid, cwd, transcriptPath, permissionMode, debugW)

			var mcpClient *mcp.Client
			// MCP server config from TOML lands in commit 2; MVP passes nil.
			mcpClient = mcp.NewClient(nil)
			_ = cfg // commit 2 consumes [mcp_servers], commit 4 consumes [hooks]

			slashHandler := slash.New(hookSender, mcpClient, os.Stdout)

			g := engine.Globals{
				Emulator:    "Codex",
				Name:        "session",
				SessionID:   sid,
				Resumed:     false,
				ThinkDelay:  rf.ThinkDelay,
				StreamDelay: rf.StreamDelay,
				ExitAfter:   rf.ExitAfter,
				AutoExit:    rf.AutoExit,
				HistoryCap:  rf.HistoryCap,
				StatusLine:  buildStatusLine(addDirs, askForApproval, sandbox, model, configOverride, agentsLine, cfg),
			}
			d := engine.Deps{
				Hooks: hookSender,
				MCP:   mcpClient,
				Slash: slashHandler,
			}
			if code := engine.Run(ctxOrBackground(cmd), g, d); code != 0 {
				return &claude.ExitError{Code: code}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.Var(&addDirs, "add-dir", "additional writable directory (repeatable)")
	f.StringVarP(&askForApproval, "ask-for-approval", "a", "", "approval policy (parsed; not modeled)")
	f.StringVarP(&cd, "cd", "C", "", "change working directory before launch")
	f.VarP(&configOverride, "config", "c", "config.toml key override KEY=VALUE (parsed; not modeled, repeatable)")
	f.StringVarP(&model, "model", "m", "", "model name (parsed; not modeled)")
	f.StringVarP(&sandbox, "sandbox", "s", "", "sandbox policy (parsed; not modeled)")
	return cmd
}

// buildStatusLine returns the one-line status summary shown under the
// banner. Empty when nothing was loaded.
func buildStatusLine(addDirs []string, ask, sandbox, model string, configOverride []string, agentsLine string, cfg *Config) string {
	var parts []string
	if model != "" {
		parts = append(parts, "model: "+model)
	}
	if sandbox != "" {
		parts = append(parts, "sandbox: "+sandbox)
	}
	if ask != "" {
		parts = append(parts, "approval: "+ask)
	}
	if len(addDirs) > 0 {
		parts = append(parts, fmt.Sprintf("dirs: %d", len(addDirs)))
	}
	if len(configOverride) > 0 {
		parts = append(parts, fmt.Sprintf("config overrides: %d", len(configOverride)))
	}
	if cfg != nil && len(cfg.MCPServers) > 0 {
		parts = append(parts, fmt.Sprintf("mcp: %d", len(cfg.MCPServers)))
	}
	if cfg != nil && len(cfg.Hooks) > 0 {
		parts = append(parts, fmt.Sprintf("hooks: %d events", len(cfg.Hooks)))
	}
	if agentsLine != "" {
		parts = append(parts, agentsLine)
	}
	return strings.Join(parts, " | ")
}

// ctxOrBackground returns cmd.Context() if set, else context.Background().
// Mirrors cmd/claude's helper.
func ctxOrBackground(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}
