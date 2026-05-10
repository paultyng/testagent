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

// flags bundles the codex subcommand-set's flag values. cobra populates
// the same pointer for the parent and all child subcommands (via
// PersistentFlags), so resume / exec read the same values as the bare
// interactive form.
type flags struct {
	AddDirs        stringSlice
	AskForApproval string
	CD             string
	ConfigOverride stringSlice
	Model          string
	Sandbox        string
}

// stringSlice implements pflag.Value for repeatable string flags
// (--add-dir, --config). Mirrors cmd/claude's helper.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }
func (s *stringSlice) Type() string       { return "stringSlice" }

// NewCommand returns the "codex" subcommand wired against the given root
// flags. The bare `testagent codex` invocation drops into an interactive
// session; `codex resume <SESSION_ID>` and `codex exec <prompt>` are
// child subcommands (commit 2 of #13's MVP plan).
func NewCommand(rf *claude.RootFlags) *cobra.Command {
	cf := &flags{}

	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Emulate the Codex CLI",
		Long: `Drives an interactive Codex-shaped session: argv compatibility,
~/.codex/config.toml loading, and AGENTS.md surfacing. Hooks and full
slash-command coverage land in follow-up commits — see #13.

Subcommands:
  codex resume <SESSION_ID>   resume a saved session
  codex exec <prompt>         non-interactive one-shot`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInteractive(cmd, rf, cf, "", false)
		},
	}

	pf := cmd.PersistentFlags()
	pf.Var(&cf.AddDirs, "add-dir", "additional writable directory (repeatable)")
	pf.StringVarP(&cf.AskForApproval, "ask-for-approval", "a", "", "approval policy (parsed; not modeled)")
	pf.StringVarP(&cf.CD, "cd", "C", "", "change working directory before launch")
	pf.VarP(&cf.ConfigOverride, "config", "c", "config.toml key override KEY=VALUE (parsed; not modeled, repeatable)")
	pf.StringVarP(&cf.Model, "model", "m", "", "model name (parsed; not modeled)")
	pf.StringVarP(&cf.Sandbox, "sandbox", "s", "", "sandbox policy (parsed; not modeled)")

	cmd.AddCommand(newResumeCommand(rf, cf))
	cmd.AddCommand(newExecCommand(rf, cf))
	return cmd
}

// runInteractive boots a codex session through the shared engine. sid
// and resumed are zero/false for fresh sessions; populated by `codex
// resume <id>`. Returns a *claude.ExitError when the engine exits
// non-zero so cobra surfaces the code at root.
func runInteractive(cmd *cobra.Command, rf *claude.RootFlags, cf *flags, sid string, resumed bool) error {
	if cf.CD != "" {
		if err := os.Chdir(cf.CD); err != nil {
			return fmt.Errorf("--cd %s: %w", cf.CD, err)
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

	if sid == "" {
		sid = newSessionID()
	}
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

	// MCP server config from TOML lands later (still part of #13 — the
	// codex matrix's [mcp_servers] row stays ✗ planned for now).
	mcpClient := mcp.NewClient(nil)
	_ = cfg // commit 2 consumes [mcp_servers], commit 4 consumes [hooks]

	slashHandler := slash.New(hookSender, mcpClient, os.Stdout)

	g := engine.Globals{
		Emulator:    "Codex",
		Name:        "session",
		SessionID:   sid,
		Resumed:     resumed,
		ThinkDelay:  rf.ThinkDelay,
		StreamDelay: rf.StreamDelay,
		ExitAfter:   rf.ExitAfter,
		AutoExit:    rf.AutoExit,
		HistoryCap:  rf.HistoryCap,
		StatusLine:  buildStatusLine(cf, agentsLine, cfg),
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
}

// buildStatusLine returns the one-line status summary shown under the
// banner. Empty when nothing was loaded.
func buildStatusLine(cf *flags, agentsLine string, cfg *Config) string {
	var parts []string
	if cf.Model != "" {
		parts = append(parts, "model: "+cf.Model)
	}
	if cf.Sandbox != "" {
		parts = append(parts, "sandbox: "+cf.Sandbox)
	}
	if cf.AskForApproval != "" {
		parts = append(parts, "approval: "+cf.AskForApproval)
	}
	if len(cf.AddDirs) > 0 {
		parts = append(parts, fmt.Sprintf("dirs: %d", len(cf.AddDirs)))
	}
	if len(cf.ConfigOverride) > 0 {
		parts = append(parts, fmt.Sprintf("config overrides: %d", len(cf.ConfigOverride)))
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
