package cursor

import (
	"testing"

	"github.com/paultyng/testagent/internal/rootflags"
)

// TestCursorCommand_AcceptsUpstreamArgv enumerates every upstream cursor-agent
// argv flag testagent claims to accept and confirms the cobra command parses
// each without error. Regression guard: when a new Cursor release adds a flag,
// real-cursor orchestrators must keep running against testagent without
// unknown-flag errors.
func TestCursorCommand_AcceptsUpstreamArgv(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		argv []string
	}{
		{"print", []string{"--print"}},
		{"output-format", []string{"--output-format", "stream-json"}},
		{"model", []string{"--model", "claude-4"}},
		{"mode", []string{"--mode", "plan"}},
		{"workspace", []string{"--workspace", "/tmp"}},
		{"force", []string{"--force"}},
		{"yolo", []string{"--yolo"}},
		{"sandbox", []string{"--sandbox", "enabled"}},
		{"approve-mcps", []string{"--approve-mcps"}},
		{"trust", []string{"--trust"}},
		{"worktree", []string{"--worktree", "/tmp/wt"}},
		{"worktree-base", []string{"--worktree-base", "main"}},
		{"plugin-dir", []string{"--plugin-dir", "/tmp/plugins"}},
		{"api-key", []string{"--api-key", "sk-test"}},
		{"header-long", []string{"--header", "X-Foo=bar"}},
		{"header-short", []string{"-H", "X-Foo=bar"}},
		{"resume", []string{"--resume", "sess-abc123"}},
		{"continue", []string{"--continue"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := NewCommand(&rootflags.Flags{})
			if err := cmd.ParseFlags(tc.argv); err != nil {
				t.Errorf("ParseFlags(%v) error: %v", tc.argv, err)
			}
		})
	}
}
