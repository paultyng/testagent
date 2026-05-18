package cursor

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/rootflags"
)

// newResumeCommand returns the "cursor resume" stub subcommand.
// An optional positional <chat-id> selects which chat to resume;
// omitting it resumes the most recent stub chat.
func newResumeCommand(_ *rootflags.Flags) *cobra.Command {
	return &cobra.Command{
		Use:          "resume [chat-id]",
		Short:        "Stub: resume a Cursor chat session",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprint(cmd.OutOrStdout(), "Resuming most recent chat: stub-chat-id-001\n")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Resuming chat: %s\n", args[0])
			}
			return nil
		},
	}
}
