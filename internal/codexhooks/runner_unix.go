//go:build unix

package codexhooks

import (
	"os/exec"
	"syscall"
)

// setProcessGroup makes cmd's shell start a new process group and
// overrides cmd.Cancel so a context cancel kills the entire group
// (shell + any children it spawned) rather than just the shell.
//
// Without this, exec.CommandContext on Linux only sends SIGKILL to the
// immediate /bin/sh process, leaving children like `sleep 5` alive
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
