package cursor

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

)

// newStatusCommand returns the "cursor status" stub subcommand.
func newStatusCommand() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:          "status",
		Short:        "Stub: show Cursor authentication status",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "text":
				fmt.Fprint(cmd.OutOrStdout(), "signed in: testagent-stub\nuser: testagent\n")
			case "json":
				out, _ := json.Marshal(map[string]any{
					"signed_in": true,
					"user":      "testagent",
				})
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", out)
			default:
				return fmt.Errorf("--format %q: want text or json", format)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "output format: text|json")
	return cmd
}
