package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
)

func TestLoadMCPConfig_StdioRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	body := `{"mcpServers":{"local":{"type":"stdio","command":"node","args":["server.js"],"env":{"K":"v"}}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := loadMCPConfig(path)
	if err != nil {
		t.Fatalf("loadMCPConfig: %v", err)
	}
	srv, ok := cfg.MCPServers["local"]
	if !ok {
		t.Fatalf("missing server \"local\"")
	}
	if srv.Type != "stdio" || srv.Command != "node" {
		t.Errorf("got %+v, want stdio command=node", srv)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "server.js" {
		t.Errorf("args = %v, want [server.js]", srv.Args)
	}
	if srv.Env["K"] != "v" {
		t.Errorf("env[K] = %q, want \"v\"", srv.Env["K"])
	}
}

func TestLoadSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		body            string
		wantNil         bool
		wantHooks       []string
		wantAllow       []string
		wantStopCommand string // expected Command field on the first Stop hook (when set)
		wantErrFrag     string
	}{
		{
			name:    "empty path returns nil",
			body:    "",
			wantNil: true,
		},
		{
			name: "claude-shaped settings",
			body: `{
				"hooks": {
					"Stop": [{"hooks": [{"type": "http", "url": "http://x/stop", "timeout": 5}]}],
					"PostToolUse": [{"hooks": [{"type": "http", "url": "http://x/tool-use", "timeout": 5}]}],
					"SessionEnd": [{"hooks": [{"type": "http", "url": "http://x/end", "timeout": 10}]}]
				},
				"permissions": {"allow": ["mcp__demo__.*"]}
			}`,
			wantHooks: []string{"PostToolUse", "SessionEnd", "Stop"},
			wantAllow: []string{"mcp__demo__.*"},
		},
		{
			// Issue #47 acceptance: a settings.json with both http and command
			// hooks loads without error and populates the Command field on the
			// command-type entry.
			name: "mixed http and command hooks",
			body: `{
				"hooks": {
					"Stop": [{"hooks": [
						{"type": "http", "url": "http://x/stop", "timeout": 5},
						{"type": "command", "command": "echo stop", "timeout": 5}
					]}],
					"PreCompact": [{"hooks": [{"type": "command", "command": "echo compacting"}]}]
				}
			}`,
			wantHooks:       []string{"PreCompact", "Stop"},
			wantStopCommand: "echo stop",
		},
		{
			name:        "invalid json",
			body:        `{not json`,
			wantErrFrag: "parsing settings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := writeTemp(t, tt.body)
			s, err := loadSettings(path)

			if tt.wantErrFrag != "" {
				if err == nil || !contains(err.Error(), tt.wantErrFrag) {
					t.Fatalf("got err=%v, want fragment %q", err, tt.wantErrFrag)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tt.wantNil {
				if s != nil {
					t.Fatalf("got %+v, want nil", s)
				}
				return
			}
			if got := len(s.Hooks); got != len(tt.wantHooks) {
				t.Fatalf("got %d hook events, want %d", got, len(tt.wantHooks))
			}
			for _, h := range tt.wantHooks {
				if _, ok := s.Hooks[h]; !ok {
					t.Errorf("missing hook event %q", h)
				}
			}
			if tt.wantAllow != nil {
				if s.Permissions == nil {
					t.Fatal("permissions nil, want allow list")
				}
				if !equalStrings(s.Permissions.Allow, tt.wantAllow) {
					t.Errorf("allow=%v, want %v", s.Permissions.Allow, tt.wantAllow)
				}
			}
			if tt.wantStopCommand != "" {
				stop := s.Hooks["Stop"]
				if len(stop) == 0 || len(stop[0].Hooks) < 2 {
					t.Fatalf("Stop hooks shape = %+v, want at least 2 entries", stop)
				}
				got := stop[0].Hooks[1].Command
				if got != tt.wantStopCommand {
					t.Errorf("Stop[0].Hooks[1].Command = %q, want %q", got, tt.wantStopCommand)
				}
				if stop[0].Hooks[1].Type != "command" {
					t.Errorf("Stop[0].Hooks[1].Type = %q, want %q", stop[0].Hooks[1].Type, "command")
				}
			}
		})
	}
}

func TestLoadMCPConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantNil     bool
		wantServers []string
		wantErrFrag string
	}{
		{
			name:    "empty path returns nil",
			body:    "",
			wantNil: true,
		},
		{
			name: "claude-shaped mcp config",
			body: `{
				"mcpServers": {
					"fileserver": {"type": "http", "url": "http://localhost:34117/mcp", "headers": {"X-Session-Id": "abc"}}
				}
			}`,
			wantServers: []string{"fileserver"},
		},
		{
			name:        "invalid json",
			body:        `{`,
			wantErrFrag: "parsing mcp config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := writeTemp(t, tt.body)
			c, err := loadMCPConfig(path)

			if tt.wantErrFrag != "" {
				if err == nil || !contains(err.Error(), tt.wantErrFrag) {
					t.Fatalf("got err=%v, want fragment %q", err, tt.wantErrFrag)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tt.wantNil {
				if c != nil {
					t.Fatalf("got %+v, want nil", c)
				}
				return
			}
			if got := len(c.MCPServers); got != len(tt.wantServers) {
				t.Fatalf("got %d servers, want %d", got, len(tt.wantServers))
			}
			for _, name := range tt.wantServers {
				if _, ok := c.MCPServers[name]; !ok {
					t.Errorf("missing server %q", name)
				}
			}
		})
	}
}

func TestLoadedStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		settings     *Settings
		mcpConfig    *MCPConfig
		systemPrompt string
		addDirs      []string
		want         string
	}{
		{
			name: "nothing loaded",
			want: "",
		},
		{
			name: "everything loaded",
			settings: &Settings{Hooks: map[string][]hooks.Matcher{
				"Stop": nil, "PostToolUse": nil, "SessionEnd": nil,
			}},
			mcpConfig:    &MCPConfig{MCPServers: map[string]mcp.Server{"fileserver": {}}},
			systemPrompt: "you are working on...",
			addDirs:      []string{"/a", "/b"},
			want:         "hooks: posttooluse, sessionend, stop | mcp: fileserver | system prompt: 21 chars | dirs: 2",
		},
		{
			name:     "only hooks",
			settings: &Settings{Hooks: map[string][]hooks.Matcher{"Stop": nil}},
			want:     "hooks: stop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := loadedStatus(tt.settings, tt.mcpConfig, tt.systemPrompt, tt.addDirs)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	if body == "" {
		return ""
	}
	path := filepath.Join(t.TempDir(), "f.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
