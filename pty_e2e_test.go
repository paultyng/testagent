// PTY-based end-to-end regression for the OSC-reply input pollution bug
// (issue #51). When testagent's TUI is driven under a real PTY, the emulator
// fires an OSC 11 background-color query during init; the reply arrives on
// stdin as `\x1b]11;rgb:…<ST|BEL>`. Bubbletea v1.3 had no OSC parser and
// interpreted the bytes as Alt+] + runes + Alt+\, polluting the keyboard
// input buffer and silently dropping the user's next slash command. Bubbletea
// v2's input parser has a proper OSC state machine that consumes the reply.
//
// These tests exercise the real terminal path: spawn testagent claude under
// a PTY, inject the OSC reply (and one CSI 6n cursor-report reply as a
// control), then type /help and assert the help output appears.
//
// Build constraint: creack/pty does not support Windows in any usable way
// for our case; skip the file there. The migration's import-path-only
// changes are still validated on Windows via `go build`/`go vet`.

//go:build !windows
// +build !windows

package main

import (
	"testing"
	"time"

	"github.com/paultyng/testagent/internal/ptytest"
)

// TestE2E_OSC11ReplyDoesNotPolluteInput is the regression for issue #51.
// Pre-fix (bubbletea v1.3.10) this test fails because the OSC 11 reply
// poisons the keyboard buffer and /help is reclassified as a regular prompt.
// Post-fix (bubbletea v2's OSC-aware input parser) the reply is consumed
// silently and the subsequent /help round-trips through the slash dispatcher.
func TestE2E_OSC11ReplyDoesNotPolluteInput(t *testing.T) {
	if testing.Short() {
		t.Skip("pty e2e: skipped in -short mode")
	}
	t.Parallel()

	bin := buildTestAgent(t)

	cases := []struct {
		name string
		// pre is the escape sequence written into stdin BEFORE /help, simulating
		// a terminal emulator's reply during bubbletea's input capture.
		pre []byte
	}{
		{
			name: "osc11_st_terminator",
			pre:  []byte("\x1b]11;rgb:0000/0000/0000\x1b\\"),
		},
		{
			name: "osc11_bel_terminator",
			pre:  []byte("\x1b]11;rgb:0000/0000/0000\x07"),
		},
		{
			// Control: a CSI cursor-position report (`ESC [ 24;1 R`) — also a
			// reply the emulator might inject. The v2 parser should consume it
			// cleanly the same way it handles the OSC reply.
			name: "csi_cursor_report",
			pre:  []byte("\x1b[24;1R"),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := ptytest.Spawn(t, bin,
				"claude",
				"-n", "OSC11",
				"--session-id", "osc-"+tc.name,
				"--think-delay", "1ms",
			)

			// Wait for the banner to appear so we know the TUI has captured
			// stdin and is ready to receive keystrokes / replies.
			if err := s.ExpectContains("/help for commands", 5*time.Second); err != nil {
				t.Fatalf("banner not seen: %v", err)
			}

			// Inject the emulator-style reply that the bug paper-cuts on.
			if err := s.Write(tc.pre); err != nil {
				t.Fatalf("write pre: %v", err)
			}

			// Type /help and submit. Under the buggy v1 parser the leading "/"
			// reaches the input buffer but its classification is corrupted by
			// the Alt+\ leftover from the OSC reply, so the slash dispatcher
			// never runs. Under v2 this is a clean round-trip.
			if err := s.Write([]byte("/help\r")); err != nil {
				t.Fatalf("write /help: %v", err)
			}

			// "slash commands" is the /help header rendered by
			// internal/slash/slash.go's cmdHelp. If we see it, the slash
			// dispatcher executed — i.e. the OSC reply did not pollute input.
			// "/clear" is a slash command listed in /help; checking for it
			// confirms the full help body was emitted rather than just the
			// header bleeding through ANSI styling.
			if err := s.ExpectContains("/clear", 10*time.Second); err != nil {
				t.Fatalf("/help output not seen — OSC reply likely polluted input: %v", err)
			}
		})
	}
}
