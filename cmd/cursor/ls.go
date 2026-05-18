package cursor

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/rootflags"
)

// newLsCommand returns the "cursor ls" stub subcommand.
func newLsCommand(_ *rootflags.Flags) *cobra.Command {
	return &cobra.Command{
		Use:          "ls",
		Short:        "Stub: list Cursor chat sessions",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), "stub-chat-id-001  2026-05-17  cursor stub chat\nstub-chat-id-002  2026-05-16  earlier stub chat\n")
			return nil
		},
	}
}
