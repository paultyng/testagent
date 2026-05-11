//go:build unix

package shellrun

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// DefaultShellCommand returns an exec.Cmd that runs command via the
// user's login shell. Honors $SHELL, falls back to /bin/sh, and uses
// `-lc` so the shell sources rc files (login + command).
func DefaultShellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, shellOrDefault("SHELL", "/bin/sh"), "-lc", command)
}

func shellOrDefault(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return fallback
}

// SetProcessGroup makes cmd's shell start a new process group and
// overrides cmd.Cancel so a context cancel kills the entire group
// (shell + any children it spawned) rather than just the shell.
//
// Without this, exec.CommandContext on Linux only sends SIGKILL to the
// immediate shell process, leaving children like `sleep 5` alive
// until they finish — defeating the timeout. macOS happens to clean
// up children differently, which masks the bug locally.
func SetProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// AfterStart is a no-op on Unix — Setpgid + group-kill in
// SetProcessGroup's cmd.Cancel already covers grandchildren. The
// Windows sibling uses this hook to assign cmd.Process to a Job
// object for equivalent kill-the-whole-tree semantics.
func AfterStart(cmd *exec.Cmd) func() {
	_ = cmd
	return func() {}
}
