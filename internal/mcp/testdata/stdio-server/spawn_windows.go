//go:build windows

package main

import (
	"fmt"
	"os"
)

func spawnSleepChild() {
	// sleep(1) isn't available on Windows; emit a stub pid on stderr
	// (matching the Unix variant's channel) so tests that parse
	// child-pid= still get a well-formed line. fmt.Println would
	// write to stdout, which is the MCP JSON-RPC pipe — that would
	// corrupt the protocol stream.
	fmt.Fprintln(os.Stderr, "child-pid=0")
}
