package cursor

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/rootflags"
)

// newLoginCommand returns the "cursor login" stub subcommand.
func newLoginCommand(_ *rootflags.Flags) *cobra.Command {
	return &cobra.Command{
		Use:          "login",
		Short:        "Stub: authenticate with Cursor (no real login)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), "Cursor authentication is stubbed in testagent. No real login performed.\n")
			return nil
		},
	}
}
