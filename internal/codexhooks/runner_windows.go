//go:build windows

package codexhooks

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// waitDelay caps how long exec.Cmd.Wait blocks for I/O completion
// after the spawned process exits. On Windows, cmd.exe-spawned children
// (like ping in tests) inherit stderr handles and keep the pipe open
// even after cmd.exe is killed via context cancel — without WaitDelay,
// Wait blocks until the grandchild dies, defeating the per-matcher
// timeout. 100ms is plenty for normal exit; well under the timeout
// slack the runner caller allows.
const waitDelay = 100 * time.Millisecond

// defaultShellCommand returns an exec.Cmd that runs command via the
// Windows command processor. Mirrors upstream codex's
// `default_shell_command`: honors %COMSPEC%, falls back to `cmd.exe`,
// and uses `/C` to run the command and exit.
func defaultShellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, shellOrDefault("COMSPEC", "cmd.exe"), "/C", command)
}

// shellOrDefault returns os.Getenv(envVar) if set and non-empty, else
// fallback. Mirrors the Unix sibling so behavior stays in sync.
func shellOrDefault(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return fallback
}

// setProcessGroup configures Windows-specific kill semantics. Windows
// has no Unix-style process groups, but cmd.exe-spawned children can
// inherit stderr/stdout pipe handles and keep them open after the
// parent cmd.exe is killed (e.g. `ping -n 6`). Set cmd.WaitDelay so
// Wait force-closes the inherited pipes after process death — without
// this, per-matcher timeouts are not honored when the shell spawns
// long-running children.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = waitDelay
}
