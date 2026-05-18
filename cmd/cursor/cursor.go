// Package cursor implements testagent's "cursor" subcommand — the Phase 1
// skeleton for Cursor CLI (cursor agent) emulation. The flag surface mirrors
// Cursor CLI 3.2.16's `cursor agent --help` output. Behavioral wiring
// (hooks, stream-json, session resume) lands in later phases; see
// cursor-adapter-plan.md at the idea root.
package cursor

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/rootflags"
)

// flags bundles the cursor subcommand's flag values. cobra populates
// the same pointer for the parent and all child subcommands (via
// PersistentFlags).
//
// Fields tagged "argv-only" are parsed for upstream-argv compatibility
// but have no behavioral effect in Phase 1.
type flags struct {
	Print        bool   // argv-only (Phase 4 stream-json)
	OutputFormat string // argv-only (Phase 4 stream-json)
	Model        string // argv-only
	Mode         string // parsed; affects banner in Phase 3
	Workspace    string // parsed; changes cwd if set
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
	Resume       string      // argv-only (resume subcommand handles in later batch)
	Continue     bool        // argv-only
}

// stringSlice implements pflag.Value for repeatable string flags.
// Mirrors cmd/codex's helper.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }
func (s *stringSlice) Type() string       { return "stringSlice" }

// NewCommand returns the "cursor" subcommand wired against the given root
// flags. The bare `testagent cursor` invocation prints a Phase 1 banner
// to stderr and exits 0.
func NewCommand(rf *rootflags.Flags) *cobra.Command {
	cf := &flags{}

	cmd := &cobra.Command{
		Use:          "cursor",
		Short:        "Emulate the Cursor CLI (cursor agent)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, rf, cf)
		},
	}

	pf := cmd.PersistentFlags()
	pf.BoolVar(&cf.Print, "print", false, "non-interactive print mode (parsed; not modeled — Phase 4)")
	pf.StringVar(&cf.OutputFormat, "output-format", "", "output format: text|json|stream-json (parsed; not modeled — Phase 4)")
	pf.StringVarP(&cf.Model, "model", "m", "", "model name (parsed; not modeled)")
	pf.StringVar(&cf.Mode, "mode", "", "agent mode: plan|ask (parsed; not modeled — Phase 3)")
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
	pf.StringVar(&cf.Resume, "resume", "", "resume session by ID (parsed; not modeled — later batch)")
	pf.BoolVar(&cf.Continue, "continue", false, "continue most recent session (parsed; not modeled)")

	cmd.AddCommand(newLoginCommand(rf))
	cmd.AddCommand(newLogoutCommand(rf))
	cmd.AddCommand(newStatusCommand(rf))
	cmd.AddCommand(newAboutCommand(rf))
	cmd.AddCommand(newModelsCommand(rf))
	cmd.AddCommand(newUpdateCommand(rf))
	cmd.AddCommand(newCreateChatCommand(rf))
	cmd.AddCommand(newResumeCommand(rf))
	cmd.AddCommand(newLsCommand(rf))
	cmd.AddCommand(newMCPCommand(rf))

	return cmd
}

// run is the Phase 1 entrypoint. It applies --workspace (cwd change) and
// prints the skeleton banner to stderr.
func run(cmd *cobra.Command, _ *rootflags.Flags, cf *flags) error {
	if cf.Workspace != "" {
		if err := os.Chdir(cf.Workspace); err != nil {
			return fmt.Errorf("--workspace %s: %w", cf.Workspace, err)
		}
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "cursor adapter — Phase 1 skeleton; interactive session lands in Phase 2")
	return nil
}
