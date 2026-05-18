package cursor

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/rootflags"
)

// newModelsCommand returns the "cursor models" stub subcommand.
func newModelsCommand(_ *rootflags.Flags) *cobra.Command {
	return &cobra.Command{
		Use:          "models",
		Short:        "Stub: list available Cursor models",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), "auto\ngrok-fast-stub\ngpt-5-stub\nclaude-sonnet-4-stub\nsonic-stub\n")
			return nil
		},
	}
}
