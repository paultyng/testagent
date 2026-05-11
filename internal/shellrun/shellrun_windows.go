//go:build windows

package shellrun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// waitDelay caps how long exec.Cmd.Wait blocks for I/O completion
// after the spawned process exits. Even with Job-object kill-on-close
// (see AfterStart) there is a brief window where stderr/stdout pipe
// readers may not have flushed; WaitDelay forces them closed so Wait
// returns promptly.
const waitDelay = 100 * time.Millisecond

// DefaultShellCommand returns an exec.Cmd that runs command via the
// Windows command processor. Honors %COMSPEC%, falls back to `cmd.exe`,
// and uses `/C` to run the command and exit.
func DefaultShellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, shellOrDefault("COMSPEC", "cmd.exe"), "/C", command)
}

func shellOrDefault(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return fallback
}

// SetProcessGroup configures pre-Start Windows kill semantics:
// WaitDelay gives stderr/stdout pipe drain a brief window after the
// Job-object close kills the tree, so Wait returns promptly.
//
// Process-tree termination on context-cancel happens via Job object
// (see AfterStart) — not via cmd.Cancel — because Cancel runs on the
// parent process only and Windows has no process-group concept.
//
// No CREATE_BREAKAWAY_FROM_JOB flag here: on GitHub Actions Windows
// runners the parent `go test` is itself inside a sandboxing Job that
// denies breakaway, so setting the flag fails CreateProcess up front
// with ERROR_ACCESS_DENIED. Windows 8+ supports nested Jobs natively,
// so AfterStart's AssignProcessToJobObject still works — cmd.exe just
// joins the parent Job AND our sub-Job. KILL_ON_JOB_CLOSE on the
// sub-Job is enough to terminate the tree.
func SetProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = waitDelay
}

// AfterStart assigns cmd.Process to a new Job object with the
// KILL_ON_JOB_CLOSE limit and overrides cmd.Cancel so a context cancel
// terminates the entire Job (cmd.exe + any descendants it spawned).
// The returned cleanup closes the Job handle on Wait completion;
// because of KILL_ON_JOB_CLOSE this kills any survivors.
//
// Best-effort: if any Job-object syscall fails (e.g. very locked-down
// sandboxes), AfterStart falls back to a no-op cleanup and the
// existing WaitDelay + Process.Kill path handles the immediate cmd.exe.
func AfterStart(cmd *exec.Cmd) func() {
	if cmd.Process == nil {
		return func() {}
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}
	}

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

	cmd.Cancel = func() error {
		if err := windows.TerminateJobObject(job, 1); err != nil {
			return fmt.Errorf("terminate job: %w", err)
		}
		return nil
	}

	return func() {
		_ = windows.CloseHandle(job)
	}
}
