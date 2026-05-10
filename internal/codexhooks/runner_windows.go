//go:build windows

package codexhooks

import (
	"context"
	"os"
	"os/exec"
)

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

// setProcessGroup is a no-op on Windows. Windows has no Unix-style
// process groups; exec.CommandContext's default kill (TerminateProcess
// on the spawned cmd.exe) is sufficient for the timeout behavior the
// Unix path uses Setpgid + group-kill to achieve.
func setProcessGroup(cmd *exec.Cmd) {}
