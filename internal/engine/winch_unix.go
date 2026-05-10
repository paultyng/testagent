//go:build unix

package engine

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyResize wires SIGWINCH (terminal resize) onto ch. Unix only —
// Windows has no SIGWINCH and uses a different console-event mechanism
// that the scanner loop intentionally does not echo.
func notifyResize(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGWINCH)
}
