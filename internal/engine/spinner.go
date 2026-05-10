// Inline spinner helpers used by the scanner-path loop. The TUI path
// drives its own spinner via the bubbles spinner component.

package engine

import (
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/paultyng/testagent/internal/render"
)

// showThinking runs the live "Thinking… (Ns)" spinner for total. On
// completion the spinner row is replaced with a static "Thought for Ns"
// marker (dim italic) that stays in scrollback above whatever the caller
// prints next. total <= 0 returns immediately without animation or marker.
// For very short delays (< 200ms) it sleeps then emits only the marker —
// the animation would be invisible.
func showThinking(out io.Writer, total time.Duration) {
	if total <= 0 {
		return
	}
	start := time.Now()

	if total >= 200*time.Millisecond {
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		deadline := start.Add(total)
		const tick = 100 * time.Millisecond

		// Print a blank line first so we have a row to repaint and a row below
		// it where the cursor can rest.
		fmt.Fprintln(out)

		for i := 0; ; i++ {
			elapsed := time.Since(start).Truncate(time.Second)
			// \033[1A moves cursor up to the spinner row; \033[2K clears it.
			// "Thinking…" wears the warm thinking token; the parenthetical
			// timer + interrupt hint stays mute so it doesn't compete.
			fmt.Fprintf(out, "\033[1A\033[2K%s%s\n",
				render.Thinking(fmt.Sprintf("%s Thinking…", frames[i%len(frames)])),
				render.MuteStyle.Render(fmt.Sprintf(" (%s · esc to interrupt)", elapsed)))
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			if remaining < tick {
				time.Sleep(remaining)
				break
			}
			time.Sleep(tick)
		}
		// Replace the spinner row with the static "Thought for Ns" marker.
		// Cursor ends on the row below, ready for the caller's echo.
		elapsed := time.Since(start).Truncate(time.Second)
		fmt.Fprintf(out, "\033[1A\033[2K%s\n", render.ThoughtMarker(fmt.Sprintf("Thought for %s", elapsed)))
		return
	}

	// Sub-200ms: skip the animation but still emit the marker so consumers
	// see a consistent shape regardless of how short the thinking phase was.
	time.Sleep(total)
	elapsed := time.Since(start).Truncate(time.Second)
	fmt.Fprintln(out, render.ThoughtMarker(fmt.Sprintf("Thought for %s", elapsed)))
}

// getTermSize returns the current terminal dimensions. Uses
// golang.org/x/term so the call is portable across Unix and Windows
// (TIOCGWINSZ on Unix, GetConsoleScreenBufferInfo on Windows). Returns
// (0, 0) when stdout is not attached to a terminal — callers must
// tolerate that without dividing by zero or panicking.
func getTermSize() (rows, cols int) {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0, 0
	}
	return rows, cols
}
