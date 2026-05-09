package main

import (
	"strings"
	"testing"
)

// TestStyleHelpers asserts each intent-named helper renders the expected
// visible text. We don't assert exact ANSI bytes — that's terminal/lipgloss
// implementation detail. We assert the human-visible glyphs and the input
// text are preserved.
func TestStyleHelpers(t *testing.T) {
	t.Parallel()

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
			t.Parallel()
			for _, want := range tc.mustHave {
				if !strings.Contains(tc.got, want) {
					t.Errorf("%s output missing %q\n--- output ---\n%s", tc.name, want, tc.got)
				}
			}
		})
	}
}
