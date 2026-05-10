//go:build !unix

package engine

import "os"

// notifyResize is a no-op on platforms without SIGWINCH (Windows).
// Terminal-resize echoes are silently disabled there; the scanner
// loop's resize goroutine sees an idle channel and never fires.
func notifyResize(ch chan<- os.Signal) {}
