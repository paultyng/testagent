// Package shellrun spawns shell commands with cross-platform
// process-tree teardown semantics. Used by hook runners that need to
// run user-configured shell commands with bounded wall-clock and
// guaranteed grandchild cleanup on context cancel.
//
// Unix: $SHELL -lc <cmd> (fallback /bin/sh). Setpgid + group SIGKILL
// on cmd.Cancel so the whole tree dies when the context expires.
//
// Windows: %COMSPEC% /C <cmd> (fallback cmd.exe). Job object with
// KILL_ON_JOB_CLOSE; cmd.Cancel calls TerminateJobObject. Best-effort —
// falls back to plain Process.Kill if any Job-object syscall fails.
//
// The three exported helpers compose: callers invoke DefaultShellCommand
// to build the *exec.Cmd, SetProcessGroup before Start, and AfterStart's
// returned cleanup with defer after Start.
package shellrun
