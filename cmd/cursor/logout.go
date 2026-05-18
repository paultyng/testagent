package cursor

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/rootflags"
)

// newLogoutCommand returns the "cursor logout" stub subcommand.
func newLogoutCommand(_ *rootflags.Flags) *cobra.Command {
	return &cobra.Command{
		Use:          "logout",
		Short:        "Stub: clear Cursor session (no real logout)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), "Cursor session cleared (stub).\n")
			return nil
		},
	}
}
