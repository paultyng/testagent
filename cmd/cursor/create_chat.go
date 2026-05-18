package cursor

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/rootflags"
)

// newCreateChatCommand returns the "cursor create-chat" stub subcommand.
func newCreateChatCommand(_ *rootflags.Flags) *cobra.Command {
	return &cobra.Command{
		Use:          "create-chat",
		Short:        "Stub: create a new Cursor chat session",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), "Created chat: stub-chat-id-001\n")
			return nil
		},
	}
}
