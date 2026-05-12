package codex

import (
	"testing"

	"github.com/paultyng/testagent/internal/rootflags"
)

// TestCodexCommand_AcceptsUpstreamArgv enumerates every upstream-codex
// argv flag testagent claims to "accept" in COMPATIBILITY.md and confirms
// the cobra command parses each without error. Regression guard: when a
// new codex release adds a flag, real-codex orchestrators must keep
// running against testagent without unknown-flag errors.
func TestCodexCommand_AcceptsUpstreamArgv(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		argv []string
	}{
		{"add-dir", []string{"--add-dir", "/tmp"}},
		{"ask-for-approval", []string{"--ask-for-approval", "never"}},
		{"cd", []string{"--cd", "/tmp"}},
		{"config-override", []string{"--config", "model_reasoning_effort=high"}},
		{"dangerously-bypass", []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{"disable", []string{"--disable", "experimental_a"}},
		{"enable", []string{"--enable", "experimental_b"}},
		{"image-short", []string{"-i", "/tmp/foo.png"}},
		{"local-provider", []string{"--local-provider", "ollama"}},
		{"model", []string{"--model", "gpt-5"}},
		{"no-alt-screen", []string{"--no-alt-screen"}},
		{"oss", []string{"--oss"}},
		{"profile", []string{"--profile", "work"}},
		{"sandbox", []string{"--sandbox", "workspace-write"}},
		{"search", []string{"--search"}},
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
