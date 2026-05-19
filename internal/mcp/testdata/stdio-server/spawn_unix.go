//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

func spawnSleepChild() {
	child := exec.Command("sleep", "600")
	child.Stdin = nil
	child.Stdout = nil
	child.Stderr = nil
	if err := child.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "fixture: spawn child: %v\n", err)
		os.Exit(2)
	}
	// Print child PID on stderr so tests can correlate.
	fmt.Fprintf(os.Stderr, "child-pid=%d\n", child.Process.Pid)
}
