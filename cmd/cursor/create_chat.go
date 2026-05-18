package cursor

import (
	"fmt"

	"github.com/spf13/cobra"

)

// newCreateChatCommand returns the "cursor create-chat" stub subcommand.
func newCreateChatCommand() *cobra.Command {
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
