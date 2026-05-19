//go:build windows

package main

import "fmt"

func spawnSleepChild() {
	// sleep(1) isn't available on Windows; emit a stub pid so tests that
	// parse child-pid= still get a well-formed line.
	fmt.Println("child-pid=0")
}
