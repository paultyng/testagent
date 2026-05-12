// testagent is a mock terminal agent for UI and integration testing.
// It emulates Claude Code (and, eventually, Codex / Gemini / etc.) without
// calling any LLM APIs.
//
// Argv shape:
//
//	testagent [global-flags] <subcommand> [subcommand-flags] [positional]
//
// Bare invocation (no subcommand keyword) defaults to the claude subcommand
// for back-compat with v0 scripts that pre-date the cobra split.
package main

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
	"github.com/paultyng/testagent/cmd/codex"
	"github.com/paultyng/testagent/internal/rootflags"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
// Falls back to "dev" when running unbuilt (`go run .`) or when the
// linker flags weren't supplied.
var version = "dev"

// knownSubcommands is the set of bare keywords that prevent default-to-claude
// prepending. Includes the cobra-reserved keywords (help, completion) and the
// hidden completion-protocol commands (__complete, __completeNoDesc) so that
// shell-completion dispatch isn't silently routed into the claude subcommand.
var knownSubcommands = map[string]bool{
	"claude":           true,
	"codex":            true,
	"help":             true,
	"completion":       true,
	"__complete":       true,
	"__completeNoDesc": true,
}

func main() {
	root := &cobra.Command{
		Use:           "testagent",
		Short:         "Fake CLI agent for testing orchestration tooling",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	rf := rootflags.Bind(root)

	root.AddCommand(claude.NewCommand(rf))
	root.AddCommand(codex.NewCommand(rf))

	root.SetArgs(defaultedArgs(os.Args[1:]))

	if err := root.Execute(); err != nil {
		var ee *claude.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.Code)
		}
		os.Exit(1)
	}
}

// defaultedArgs prepends "claude" to argv when no recognized subcommand or
// passthrough flag is present anywhere in args, so back-compat invocations
// like `testagent --history-cap 500 --resume sid-x` still resolve to the
// claude subcommand AND interleaved cases like `testagent --history-cap 5
// claude` route correctly to the explicit subcommand. Searching the whole
// arg list rather than the first non-flag token avoids needing to know
// which flags take values (cobra handles flag/value pairing during real
// parsing).
func defaultedArgs(args []string) []string {
	if len(args) == 0 {
		// Bare `testagent` shows root help. Don't prepend.
		return args
	}
	for _, tok := range args {
		if isRootPassthroughFlag(tok) {
			return args
		}
		if knownSubcommands[tok] {
			return args
		}
	}
	return append([]string{"claude"}, args...)
}

// isRootPassthroughFlag reports whether tok is a help or version flag that
// should route to the root command rather than be steered into claude.
// `-v` is cobra's auto-added short form of `--version` (root.Version is set
// in main).
func isRootPassthroughFlag(tok string) bool {
	switch tok {
	case "-h", "--help", "-v", "--version":
		return true
	}
	return false
}
