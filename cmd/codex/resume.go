package codex

import (
	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
)

// newResumeCommand returns the `codex resume <SESSION_ID>` subcommand.
// Boots an interactive session keyed to the supplied session ID, with
// `engine.Globals.Resumed=true` so SessionStart fires with
// `source="resume"` once the codex hook runner is wired in commit 4.
func newResumeCommand(rf *claude.RootFlags, cf *flags) *cobra.Command {
	return &cobra.Command{
		Use:          "resume <SESSION_ID>",
		Short:        "Resume a saved Codex session",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInteractive(cmd, rf, cf, args[0], true)
		},
	}
}
