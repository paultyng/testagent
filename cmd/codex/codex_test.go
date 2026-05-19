package codex

import (
	"testing"

	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/rootflags"
)

func TestCodexMCPServer_ToCoreServer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   MCPServer
		want mcp.Server
	}{
		{
			name: "http",
			in:   MCPServer{Type: "http", URL: "https://x", Headers: map[string]string{"H": "v"}},
			want: mcp.Server{Type: "http", URL: "https://x", Headers: map[string]string{"H": "v"}},
		},
		{
			name: "stdio",
			in:   MCPServer{Type: "stdio", Command: "node", Args: []string{"s.js"}, Env: map[string]string{"K": "v"}},
			want: mcp.Server{Type: "stdio", Command: "node", Args: []string{"s.js"}, Env: map[string]string{"K": "v"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.in.toCoreServer()
			if got.Type != tc.want.Type || got.URL != tc.want.URL || got.Command != tc.want.Command {
				t.Errorf("toCoreServer = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCodexMCPServersFromConfig(t *testing.T) {
	t.Parallel()

	t.Run("nil cfg yields nil", func(t *testing.T) {
		t.Parallel()
		if got := codexMCPServersFromConfig(nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("empty MCPServers yields nil", func(t *testing.T) {
		t.Parallel()
		if got := codexMCPServersFromConfig(&Config{}); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("projects all servers", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			MCPServers: map[string]MCPServer{
				"http-srv":  {Type: "http", URL: "https://a"},
				"stdio-srv": {Type: "stdio", Command: "node", Args: []string{"s.js"}},
			},
		}
		got := codexMCPServersFromConfig(cfg)
		if len(got) != 2 {
			t.Fatalf("got %d servers, want 2: %+v", len(got), got)
		}
		if s, ok := got["http-srv"]; !ok || s.URL != "https://a" {
			t.Errorf("http-srv not projected correctly: %+v", got)
		}
		if s, ok := got["stdio-srv"]; !ok || s.Command != "node" {
			t.Errorf("stdio-srv not projected correctly: %+v", got)
		}
	})
}

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
