package cursor

import (
	"fmt"

	"github.com/spf13/cobra"

)

// newUpdateCommand returns the "cursor update" stub subcommand.
func newUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:          "update",
		Short:        "Stub: update Cursor (always up to date)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), "Already up to date (testagent stub).\n")
			return nil
		},
	}
}
