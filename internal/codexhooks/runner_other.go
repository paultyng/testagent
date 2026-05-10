//go:build !unix

package codexhooks

import "os/exec"

// setProcessGroup is a no-op on non-Unix platforms. The runner already
// shells out via /bin/sh so it is effectively Unix-only at runtime;
// this stub just keeps the package compiling.
func setProcessGroup(cmd *exec.Cmd) {}
