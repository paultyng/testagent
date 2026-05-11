package codex

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/rootflags"
)

// newExecCommand returns the `codex exec <prompt>` subcommand. Non-
// interactive one-shot — codex's analog of `claude --print`. MVP emits
// text only; JSON / stream-json frame shapes are deferred to #32.
//
// Lifecycle: read prompt → produce echo → exit. Hooks are intentionally
// not fired here in MVP — once exec grows real work this will mirror
// claude/print.go's SessionStart → UserPromptSubmit → Stop → SessionEnd
// sequence against codexhooks.Runner.
func newExecCommand(rf *rootflags.RootFlags, cf *flags) *cobra.Command {
	return &cobra.Command{
		Use:          "exec [prompt]",
		Short:        "Run Codex non-interactively (one-shot)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cf.CD != "" {
				if err := os.Chdir(cf.CD); err != nil {
					return fmt.Errorf("--cd %s: %w", cf.CD, err)
				}
			}

			prompt := strings.TrimSpace(strings.Join(args, " "))
			if prompt == "" {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read prompt from stdin: %w", err)
				}
				prompt = strings.TrimSpace(string(b))
			}
			if prompt == "" {
				return errors.New("codex exec requires a prompt (positional arg or stdin)")
			}

			// TODO: when exec gains real work (hook runner, MCP, model dispatch),
			// thread ctxOrBackground(cmd) through so SIGINT/cancellation propagates.
			fmt.Fprintf(os.Stdout, "[%s] %s\n", "session", prompt)
			return nil
		},
	}
}
