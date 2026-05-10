//go:build unix

package codexhooks

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// defaultShellCommand returns an exec.Cmd that runs command via the
// user's login shell. Mirrors upstream codex's `default_shell_command`:
// honors $SHELL, falls back to /bin/sh, and uses `-lc` so the shell
// sources rc files (login + command).
func defaultShellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, shellOrDefault("SHELL", "/bin/sh"), "-lc", command)
}

// shellOrDefault returns os.Getenv(envVar) if set and non-empty, else
// fallback. Kept as a tiny helper so the Windows sibling can mirror it
// with no behavioral skew.
func shellOrDefault(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return fallback
}

// setProcessGroup makes cmd's shell start a new process group and
// overrides cmd.Cancel so a context cancel kills the entire group
// (shell + any children it spawned) rather than just the shell.
//
// Without this, exec.CommandContext on Linux only sends SIGKILL to the
// immediate shell process, leaving children like `sleep 5` alive
// until they finish — defeating the timeout. macOS happens to clean
// up children differently, which masks the bug locally.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid → signal the whole process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
