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
	"time"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
	"github.com/paultyng/testagent/cmd/codex"
)

// knownSubcommands is the set of bare keywords that prevent default-to-claude
// prepending. Includes the cobra-reserved keywords (help, completion) and the
// hidden completion-protocol commands (__complete, __completeNoDesc) so that
// shell-completion dispatch isn't silently routed into the claude subcommand.
var knownSubcommands = map[string]bool{
	"claude":            true,
	"codex":             true,
	"help":              true,
	"completion":        true,
	"__complete":        true,
	"__completeNoDesc":  true,
}

func main() {
	rf := &claude.RootFlags{}
	root := &cobra.Command{
		Use:           "testagent",
		Short:         "Fake CLI agent for testing orchestration tooling",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	pf := root.PersistentFlags()
	pf.IntVar(&rf.HistoryCap, "history-cap", 1000, "TUI history cap (0 = unlimited)")
	pf.BoolVarP(&rf.Verbose, "verbose", "v", false, "log hook activity to stderr")
	pf.DurationVar(&rf.AutoExit, "auto-exit", 0, "auto-exit after duration (0 = disabled)")
	pf.IntVar(&rf.ExitAfter, "exit-after", 0, "auto-exit after N interactions (0 = never)")
	pf.DurationVar(&rf.ThinkDelay, "think-delay", 2*time.Second, "default thinking-spinner duration per turn (override per-turn with /think)")
	pf.DurationVar(&rf.StreamDelay, "stream-delay", 30*time.Millisecond, "default per-token stream interval for the response (override per-turn with /stream)")

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
// help flag is present anywhere in args, so back-compat invocations like
// `testagent --history-cap 500 --resume sid-x` still resolve to the claude
// subcommand AND interleaved cases like `testagent --history-cap 5 claude`
// route correctly to the explicit subcommand. Searching the whole arg list
// rather than the first non-flag token avoids needing to know which flags
// take values (cobra handles flag/value pairing during real parsing).
func defaultedArgs(args []string) []string {
	if len(args) == 0 {
		// Bare `testagent` shows root help. Don't prepend.
		return args
	}
	for _, tok := range args {
		if isHelpFlag(tok) {
			return args
		}
		if knownSubcommands[tok] {
			return args
		}
	}
	return append([]string{"claude"}, args...)
}

// isHelpFlag reports whether tok is a help flag that should route to the
// root command rather than be steered into claude.
func isHelpFlag(tok string) bool {
	return tok == "-h" || tok == "--help"
}
