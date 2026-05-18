package cursor

import (
	"fmt"

	"github.com/spf13/cobra"

)

// newLoginCommand returns the "cursor login" stub subcommand.
func newLoginCommand() *cobra.Command {
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
