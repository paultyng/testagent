package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
)

// newExecCommand returns the `codex exec <prompt>` subcommand. Non-
// interactive one-shot — codex's analog of `claude --print`. MVP emits
// text only; JSON / stream-json frame shapes are deferred to #32.
//
// Lifecycle: read prompt → produce echo → exit. Hooks are not fired
// in MVP (commit 4 wires the codex shell-command hook runner); when
// they are wired this is the analog of claude/print.go's
// SessionStart → UserPromptSubmit → Stop → SessionEnd sequence.
func newExecCommand(rf *claude.RootFlags, cf *flags) *cobra.Command {
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

			_ = ctxOrBackgroundExec(cmd) // ctx will plumb into the hook runner in commit 4
			fmt.Fprintf(os.Stdout, "[%s] %s\n", "session", prompt)
			return nil
		},
	}
}

// ctxOrBackgroundExec mirrors codex.go's helper but lives in this file
// so commit 4 can drop a runner-aware variant in without churning the
// import set in codex.go.
func ctxOrBackgroundExec(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}
