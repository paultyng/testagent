package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestStyleHelpers asserts each intent-named helper preserves its visible
// glyphs and input text, AND emits ANSI escape bytes when the lipgloss
// color profile permits styling. We don't pin exact bytes — that's
// terminal/lipgloss implementation detail — but a regression that makes
// a helper accidentally return an unstyled string would still fail.
func TestStyleHelpers(t *testing.T) {
	// Force TrueColor so styling output is deterministic regardless of the
	// CI runner's TERM / NO_COLOR / CLICOLOR env. Cannot t.Parallel at the
	// package level here because SetColorProfile is global.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(2) // termenv.TrueColor
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	cases := []struct {
		name     string
		got      string
		mustHave []string
	}{
		{name: "renderPrompt", got: renderPrompt(), mustHave: []string{">"}},
		{name: "renderEcho", got: renderEcho("agent", "hi"), mustHave: []string{"[agent]", "hi"}},
		{name: "renderLifecycle", got: renderLifecycle("mcp connected: 3 tools"), mustHave: []string{"[mcp connected: 3 tools]"}},
		{name: "renderLifecycleWarn", got: renderLifecycleWarn("hook UserPromptSubmit error: timeout"), mustHave: []string{"[hook UserPromptSubmit error: timeout]"}},
		{name: "renderToolHeader", got: renderToolHeader("▶ ", "read_file"), mustHave: []string{"▶ read_file"}},
		{name: "renderResultOk", got: renderResultOk(), mustHave: []string{"✓"}},
		{name: "renderResultErr", got: renderResultErr(), mustHave: []string{"✗"}},
		{name: "renderThoughtMarker", got: renderThoughtMarker("Thought for 5s"), mustHave: []string{"Thought for 5s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range tc.mustHave {
				if !strings.Contains(tc.got, want) {
					t.Errorf("%s output missing %q\n--- output ---\n%s", tc.name, want, tc.got)
				}
			}
			// ESC byte (0x1b) means lipgloss actually emitted SGR codes.
			// Catches regressions where a helper drops styling.
			if !strings.Contains(tc.got, "\x1b") {
				t.Errorf("%s output has no ANSI escape — styling was dropped\n--- output ---\n%q", tc.name, tc.got)
			}
		})
	}
}
