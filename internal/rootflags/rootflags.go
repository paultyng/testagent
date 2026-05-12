// Package rootflags holds the persistent flags cobra binds on the root
// command. Owned by main; the pointer is populated by cobra before any
// subcommand's RunE fires. Both cmd/claude and cmd/codex consume the
// type without one subcommand importing the other.
package rootflags

import (
	"time"

	"github.com/spf13/cobra"
)

// Flags are the testagent-wide persistent flags. None are vendor-
// specific; both the claude and codex subcommands borrow the same
// pointer and propagate the values into engine.Globals at RunE time.
type Flags struct {
	HistoryCap  int
	Verbose     bool
	AutoExit    time.Duration
	ExitAfter   int
	ThinkDelay  time.Duration
	StreamDelay time.Duration
}

// Bind wires the persistent flags onto cmd and returns a pointer to
// the populated Flags. Cobra fills in values during Execute before
// any subcommand's RunE runs.
func Bind(cmd *cobra.Command) *Flags {
	rf := &Flags{}
	pf := cmd.PersistentFlags()
	pf.IntVar(&rf.HistoryCap, "history-cap", 1000, "TUI history cap (0 = unlimited)")
	// --verbose intentionally has no short form: cobra reserves `-v` for
	// `--version` once root.Version is set, and a binary-identity flag is
	// the more useful default for that letter.
	pf.BoolVar(&rf.Verbose, "verbose", false, "log hook activity to stderr")
	pf.DurationVar(&rf.AutoExit, "auto-exit", 0, "auto-exit after duration (0 = disabled)")
	pf.IntVar(&rf.ExitAfter, "exit-after", 0, "auto-exit after N interactions (0 = never)")
	pf.DurationVar(&rf.ThinkDelay, "think-delay", 2*time.Second, "default thinking-spinner duration per turn (override per-turn with /think)")
	pf.DurationVar(&rf.StreamDelay, "stream-delay", 30*time.Millisecond, "default per-token stream interval for the response (override per-turn with /stream)")
	return rf
}
