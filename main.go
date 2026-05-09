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
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
	"github.com/paultyng/testagent/cmd/codex"
)

// knownSubcommands is the set of bare keywords that prevent default-to-claude
// prepending. cobra also reserves "help" and "completion" automatically; both
// are listed here for clarity.
var knownSubcommands = map[string]bool{
	"claude":     true,
	"codex":      true,
	"help":       true,
	"completion": true,
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
	pf.DurationVar(&rf.Delay, "delay", 3*time.Second, "simulated thinking delay before response (also the default for /think)")

	root.AddCommand(claude.NewCommand(rf))
	root.AddCommand(codex.NewCommand(rf))

	root.SetArgs(defaultedArgs(os.Args[1:]))

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// defaultedArgs prepends "claude" to argv when no recognized subcommand is
// supplied, so back-compat invocations like `testagent --history-cap 500
// --resume sid-x` still resolve to the claude subcommand. Lone --help / -h
// (and --version / -V once it exists) pass through to root help so users
// see the full subcommand list.
func defaultedArgs(args []string) []string {
	if len(args) == 0 {
		// Bare `testagent` shows root help. Don't prepend.
		return args
	}
	first := args[0]
	if knownSubcommands[first] {
		return args
	}
	if isHelpOrVersionFlag(first) {
		return args
	}
	return append([]string{"claude"}, args...)
}

// isHelpOrVersionFlag reports whether tok is a help or version flag that
// should route to root command rather than be steered into claude.
func isHelpOrVersionFlag(tok string) bool {
	switch tok {
	case "-h", "--help", "-V", "--version":
		return true
	}
	return false
}
