// Package codex is a placeholder for the Codex CLI emulation. It exists to
// validate the per-vendor cobra-subcommand contract — not to do any work.
// The real codex behavior lands in a follow-up issue.
package codex

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
)

// NewCommand returns the "codex" subcommand stub. Accepts the same RootFlags
// pointer the claude command does so the dispatch shape is consistent across
// vendors; the stub itself ignores them.
func NewCommand(_ *claude.RootFlags) *cobra.Command {
	var (
		sessionID string
		model     string
	)
	cmd := &cobra.Command{
		Use:          "codex",
		Short:        "(stub) emulate Codex CLI — not yet implemented",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "codex (stub): not yet implemented")
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "")
	cmd.Flags().StringVar(&model, "model", "", "")
	return cmd
}
