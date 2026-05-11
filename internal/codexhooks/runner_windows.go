//go:build windows

package codexhooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// waitDelay caps how long exec.Cmd.Wait blocks for I/O completion
// after the spawned process exits. Even with Job-object kill-on-close
// (see afterStart) there is a brief window where stderr/stdout pipe
// readers may not have flushed; WaitDelay forces them closed so Wait
// returns promptly.
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

// setProcessGroup configures pre-Start Windows kill semantics. Two
// pieces matter: (1) CREATE_BREAKAWAY_FROM_JOB lets afterStart assign
// the spawned cmd.exe to our own Job object even when the test
// runner / parent process is already in a Job (e.g. under
// `go test` itself, which is sometimes nested in one); (2)
// WaitDelay gives stderr/stdout pipe drain a brief window after the
// Job-object close kills the tree, so Wait returns promptly.
//
// Process-tree termination on context-cancel happens via Job object
// (see afterStart) — not via cmd.Cancel — because Cancel runs on the
// parent process only and Windows has no process-group concept.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = waitDelay
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_BREAKAWAY_FROM_JOB
}

// afterStart assigns cmd.Process to a new Job object with the
// KILL_ON_JOB_CLOSE limit and overrides cmd.Cancel so a context cancel
// terminates the entire Job (cmd.exe + any descendants it spawned).
// The returned cleanup closes the Job handle on Wait completion;
// because of KILL_ON_JOB_CLOSE this kills any survivors. Without
// this, Windows has no equivalent of Unix process-group SIGKILL and
// hook timeouts can leave orphaned grandchildren running.
//
// Best-effort: if any Job-object syscall fails (e.g. very locked-down
// sandboxes), afterStart falls back to a no-op cleanup and the
// existing WaitDelay + Process.Kill path handles the immediate
// cmd.exe — same as before #52.
func afterStart(cmd *exec.Cmd) func() {
	if cmd.Process == nil {
		return func() {}
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}
	}

	// Set the kill-on-close limit. The struct shape is
	// JOBOBJECT_EXTENDED_LIMIT_INFORMATION; only the LimitFlags
	// field matters here.
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return func() {}
	}

	// cmd.Process.Pid is the spawned PID; OpenProcess gets us a
	// Win32 handle with the rights needed to AssignProcessToJobObject.
	ph, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return func() {}
	}
	defer windows.CloseHandle(ph)

	if err := windows.AssignProcessToJobObject(job, ph); err != nil {
		_ = windows.CloseHandle(job)
		return func() {}
	}

	// Replace cmd.Cancel: on context cancel, terminate the entire
	// Job (atomic, kills cmd.exe + grandchildren in one syscall)
	// rather than the default Process.Kill which only targets the
	// immediate cmd.exe handle.
	cmd.Cancel = func() error {
		if err := windows.TerminateJobObject(job, 1); err != nil {
			return fmt.Errorf("terminate job: %w", err)
		}
		return nil
	}

	return func() {
		// CloseHandle here triggers JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		// killing any survivors that outlasted the immediate cmd.exe
		// exit. Safe to call after a successful Wait — the Job is
		// already empty in the happy case.
		_ = windows.CloseHandle(job)
	}
}
