// Package cursor implements testagent's "cursor" subcommand — the v1
// drop-in fake for Cursor CLI (cursor agent). Vendor-specific knobs
// (cursor-shaped flags, the .cursor/{mcp,hooks}.json loaders,
// .cursor/rules/*.mdc surfacing, AGENTS.md surfacing, stream-json
// emission) live here; the shared engine loop in internal/engine drives
// the actual interactive session. Typed tool_call frames in stream-json
// remain a follow-up — see cursor-adapter-plan.md.
package cursor

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
	"github.com/paultyng/testagent/internal/cursorhooks"
	"github.com/paultyng/testagent/internal/engine"
	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/rootflags"
	"github.com/paultyng/testagent/internal/slash"
)

// flags bundles the cursor subcommand's flag values. cobra populates
// the same pointer for the parent and all child subcommands (via
// PersistentFlags).
//
// Fields tagged "argv-only" are parsed for upstream-argv compatibility
// but have no behavioral effect in Phase 1.
type flags struct {
	Print        bool   // supported (routes to runPrintMode)
	OutputFormat string // supported (text|json|stream-json honored in runPrint)
	Model        string // argv-only
	Mode         string // argv-only (banner-state toggle stubbed in /plan, /ask)
	Workspace    string // supported (resolved in resolveWorkspace; chdirs the bare REPL entrypoint)
	Force        bool   // argv-only
	Yolo         bool   // argv-only
	Sandbox      string // argv-only
	ApproveMCPs  bool   // argv-only
	Trust        bool   // argv-only
	Worktree     string // argv-only
	WorktreeBase string // argv-only
	PluginDir    stringSlice
	APIKey       string      // argv-only
	Header       stringSlice // argv-only
	Resume       string      // argv-only (persistent flag; positional-arg form handled by the resume subcommand)
	Continue     bool        // argv-only
}

// stringSlice implements pflag.Value for repeatable string flags.
// Mirrors cmd/codex's helper.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }
func (s *stringSlice) Type() string       { return "stringSlice" }

// NewCommand returns the "cursor" subcommand wired against the given root
// flags. The bare `testagent cursor` invocation drops into the shared
// engine loop; subcommands (login/logout/status/about/models/update/
// create-chat/resume/ls/mcp) handle one-shot CLI surface.
func NewCommand(rf *rootflags.Flags) *cobra.Command {
	cf := &flags{}

	cmd := &cobra.Command{
		Use:          "cursor",
		Short:        "Emulate the Cursor CLI (cursor agent)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cf.Print {
				return runPrintMode(cmd, rf, cf, args)
			}
			return runInteractive(cmd, rf, cf, "", false)
		},
	}

	pf := cmd.PersistentFlags()
	pf.BoolVar(&cf.Print, "print", false, "non-interactive print mode; emit one turn and exit")
	pf.StringVar(&cf.OutputFormat, "output-format", "", "output format: text|json|stream-json (honored when --print is set)")
	pf.StringVarP(&cf.Model, "model", "m", "", "model name (parsed; not modeled)")
	pf.StringVar(&cf.Mode, "mode", "", "agent mode: plan|ask (parsed; banner-state toggle stubbed in /plan, /ask)")
	pf.StringVar(&cf.Workspace, "workspace", "", "change working directory before launch")
	pf.BoolVar(&cf.Force, "force", false, "skip confirmation prompts (parsed; not modeled)")
	pf.BoolVar(&cf.Yolo, "yolo", false, "skip all confirmations (parsed; not modeled)")
	pf.StringVar(&cf.Sandbox, "sandbox", "", "sandbox policy: enabled|disabled (parsed; not modeled)")
	pf.BoolVar(&cf.ApproveMCPs, "approve-mcps", false, "auto-approve all MCPs (parsed; not modeled)")
	pf.BoolVar(&cf.Trust, "trust", false, "trust all tools without confirmation (parsed; not modeled)")
	pf.StringVar(&cf.Worktree, "worktree", "", "worktree path (parsed; not modeled)")
	pf.StringVar(&cf.WorktreeBase, "worktree-base", "", "worktree base ref (parsed; not modeled)")
	pf.Var(&cf.PluginDir, "plugin-dir", "plugin directory (parsed; not modeled, repeatable)")
	pf.StringVar(&cf.APIKey, "api-key", "", "Cursor API key (parsed; not modeled)")
	pf.VarP(&cf.Header, "header", "H", "HTTP header K=V (parsed; not modeled, repeatable)")
	pf.StringVar(&cf.Resume, "resume", "", "resume session by ID (persistent flag; positional-arg form handled by the resume subcommand)")
	pf.BoolVar(&cf.Continue, "continue", false, "continue most recent session (parsed; not modeled)")

	cmd.AddCommand(newLoginCommand())
	cmd.AddCommand(newLogoutCommand())
	cmd.AddCommand(newStatusCommand())
	cmd.AddCommand(newAboutCommand())
	cmd.AddCommand(newModelsCommand())
	cmd.AddCommand(newUpdateCommand())
	cmd.AddCommand(newCreateChatCommand())
	cmd.AddCommand(newResumeCommand())
	cmd.AddCommand(newLsCommand())
	cmd.AddCommand(newMCPCommand(rf, cf))

	return cmd
}

// runPrintMode boots cursor's --print one-shot path. Builds the same
// hook runner + MCP client + status-line inputs as runInteractive, then
// dispatches to runPrint (defined in print.go) instead of engine.Run.
// Stream-json shape is documented at cursor.com/docs/cli/reference/output-format.
func runPrintMode(cmd *cobra.Command, rf *rootflags.Flags, cf *flags, args []string) error {
	if cf.Workspace != "" {
		if err := os.Chdir(cf.Workspace); err != nil {
			return fmt.Errorf("--workspace %s: %w", cf.Workspace, err)
		}
	}
	cwd, _ := os.Getwd()

	cfg, err := loadConfig(cwd)
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

	runner := cursorhooks.NewRunner(matchersFromConfig(hooksOrNil(cfg)), sid, cwd, transcriptPath, permissionMode, debugW)
	mcpClient := mcp.NewClient(enabledServersFromConfig(cfg))
	mcpClient.SetDebugWriter(debugW)

	ctx := cmd.Context()
	if err := mcpClient.Connect(ctx); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "testagent: mcp Connect: %v\n", err)
	}

	code := runPrint(ctx, printOptions{
		name:         "session",
		sessionID:    sid,
		cwd:          cwd,
		model:        cf.Model,
		outputFormat: cf.OutputFormat,
		positional:   args,
		resumed:      cf.Resume != "",
		hooks:        runner,
		mcp:          mcpClient,
		stderr:       cmd.ErrOrStderr(),
	}, cmd.InOrStdin(), cmd.OutOrStdout())
	if code != 0 {
		return &claude.ExitError{Code: code}
	}
	return nil
}

// runInteractive boots a cursor session through the shared engine. sid
// and resumed are zero/false for fresh sessions; populated when /resume
// or a future "cursor resume <id>" lands. Returns a *claude.ExitError when
// the engine exits non-zero so cobra surfaces the code at root.
func runInteractive(cmd *cobra.Command, rf *rootflags.Flags, cf *flags, sid string, resumed bool) error {
	if cf.Workspace != "" {
		if err := os.Chdir(cf.Workspace); err != nil {
			return fmt.Errorf("--workspace %s: %w", cf.Workspace, err)
		}
	}
	cwd, _ := os.Getwd()

	cfg, err := loadConfig(cwd)
	if err != nil {
		return err
	}
	agentsLine, err := loadAgentsMD(cwd)
	if err != nil {
		return err
	}
	rules, err := loadRules(cwd)
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

	hookCfg := hooksOrNil(cfg)
	runner := cursorhooks.NewRunner(matchersFromConfig(hookCfg), sid, cwd, transcriptPath, permissionMode, debugW)

	mcpClient := mcp.NewClient(enabledServersFromConfig(cfg))
	mcpClient.SetDebugWriter(debugW)
	slashHandler := slash.New(runner, mcpClient, os.Stdout)

	g := engine.Globals{
		Emulator:    "Cursor",
		Name:        "session",
		SessionID:   sid,
		Resumed:     resumed,
		ThinkDelay:  rf.ThinkDelay,
		StreamDelay: rf.StreamDelay,
		ExitAfter:   rf.ExitAfter,
		AutoExit:    rf.AutoExit,
		HistoryCap:  rf.HistoryCap,
		StatusLine:  buildStatusLine(cf, agentsLine, cfg, rules),
	}
	d := engine.Deps{
		Hooks: runner,
		MCP:   mcpClient,
		Slash: slashHandler,
	}
	if code := engine.Run(cmd.Context(), g, d); code != 0 {
		return &claude.ExitError{Code: code}
	}
	return nil
}

// hooksOrNil returns cfg.Hooks or nil. Lifts the nil check out of
// runInteractive so matchersFromConfig sees a stable shape.
func hooksOrNil(cfg *Config) *HooksConfig {
	if cfg == nil {
		return nil
	}
	return cfg.Hooks
}

// buildStatusLine returns the one-line status summary shown under the
// banner. Empty when nothing was loaded. Mirrors codex's buildStatusLine
// shape so orchestrators see a consistent banner across vendors.
func buildStatusLine(cf *flags, agentsLine string, cfg *Config, rules []RuleFile) string {
	var parts []string
	if cf.Model != "" {
		parts = append(parts, "model: "+cf.Model)
	}
	if cf.Mode != "" {
		parts = append(parts, "mode: "+cf.Mode)
	}
	if cf.Sandbox != "" {
		parts = append(parts, "sandbox: "+cf.Sandbox)
	}
	if cfg != nil && cfg.MCP != nil && len(cfg.MCP.MCPServers) > 0 {
		parts = append(parts, fmt.Sprintf("mcp: %d", len(cfg.MCP.MCPServers)))
	}
	if cfg != nil && cfg.Hooks != nil && len(cfg.Hooks.Hooks) > 0 {
		parts = append(parts, fmt.Sprintf("hooks: %d events", len(cfg.Hooks.Hooks)))
	}
	if rulesLine := rulesStatusLine(rules); rulesLine != "" {
		parts = append(parts, rulesLine)
	}
	if agentsLine != "" {
		parts = append(parts, agentsLine)
	}
	return strings.Join(parts, " | ")
}
