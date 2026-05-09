// Token-by-token echo streaming for the scanner-path prompt loop. The TUI
// path drives its own state-machine (streamChunkMsg + tea.Tick) since
// bubbletea redraws on Msg arrival, not wall-clock writes.

package engine

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/paultyng/testagent/internal/render"
)

// streamEcho writes a styled `[name] message` echo to out, pacing each
// whitespace-delimited token with perToken delay between writes. Tokens
// come from strings.Fields so multi-space runs in the message are
// collapsed to single spaces — the assembled bytes match
// Println(render.Echo(...)) for any normally-spaced input, which is
// what existing e2e substring assertions exercise. Trailing newline
// always fires.
func streamEcho(out io.Writer, name, message string, perToken time.Duration) {
	// Header "[name] " is rendered + emitted as one chunk so token timing
	// only paces the message body — name is per-turn fixed, no point
	// staggering it.
	fmt.Fprint(out, render.EchoHeader(name)+" ")

	tokens := strings.Fields(message)
	for i, t := range tokens {
		if i > 0 {
			fmt.Fprint(out, " ")
		}
		fmt.Fprint(out, t)
		if perToken > 0 && i < len(tokens)-1 {
			time.Sleep(perToken)
		}
	}
	fmt.Fprintln(out)
}
