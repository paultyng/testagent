package cursor

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

)

// newAboutCommand returns the "cursor about" stub subcommand.
func newAboutCommand() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:          "about",
		Short:        "Stub: show Cursor agent version information",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "text":
				fmt.Fprint(cmd.OutOrStdout(), "cursor agent (testagent stub)\nversion: 3.2.16-stub\n")
			case "json":
				out, _ := json.Marshal(map[string]any{
					"name":    "cursor agent (testagent stub)",
					"version": "3.2.16-stub",
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
