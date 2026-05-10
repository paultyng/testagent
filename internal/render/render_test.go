package render

import (
	"strings"
	"testing"
)

// TestStyleHelpers asserts each intent-named helper preserves its visible
// glyphs and input text, AND emits ANSI escape bytes for styled tokens. In
// lipgloss v2 Style.Render() always emits full-fidelity ANSI (color
// downsampling moved to the Writer layer), so no SetColorProfile shim is
// needed and the test is safe to run in parallel.
func TestStyleHelpers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		got      string
		mustHave []string
	}{
		{name: "Prompt", got: Prompt(), mustHave: []string{">"}},
		{name: "Echo", got: Echo("agent", "hi"), mustHave: []string{"[agent]", "hi"}},
		{name: "Lifecycle", got: Lifecycle("mcp connected: 3 tools"), mustHave: []string{"[mcp connected: 3 tools]"}},
		{name: "LifecycleWarn", got: LifecycleWarn("hook UserPromptSubmit error: timeout"), mustHave: []string{"[hook UserPromptSubmit error: timeout]"}},
		{name: "ToolHeader", got: ToolHeader("▶ ", "read_file"), mustHave: []string{"▶ read_file"}},
		{name: "ResultOk", got: ResultOk(), mustHave: []string{"✓"}},
		{name: "ResultErr", got: ResultErr(), mustHave: []string{"✗"}},
		{name: "ThoughtMarker", got: ThoughtMarker("Thought for 5s"), mustHave: []string{"Thought for 5s"}},
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
