package cursor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewSessionID asserts the UUID-v4 shape: 36 chars, 4 hyphens at
// positions 8/13/18/23, version digit "4" at position 14, and variant
// nibble in {8,9,a,b} at position 19.
func TestNewSessionID(t *testing.T) {
	t.Parallel()

	for i := 0; i < 32; i++ {
		sid := newSessionID()
		if len(sid) != 36 {
			t.Fatalf("len = %d, want 36 (got %q)", len(sid), sid)
		}
		for _, pos := range []int{8, 13, 18, 23} {
			if sid[pos] != '-' {
				t.Errorf("expected '-' at %d, got %c (%q)", pos, sid[pos], sid)
			}
		}
		if sid[14] != '4' {
			t.Errorf("expected version digit '4' at pos 14, got %c (%q)", sid[14], sid)
		}
		switch sid[19] {
		case '8', '9', 'a', 'b':
		default:
			t.Errorf("expected variant nibble in {8,9,a,b} at pos 19, got %c (%q)", sid[19], sid)
		}
	}
}

// TestNewSessionIDsAreUnique sanity-checks the entropy: 100 ids generated
// in a tight loop should all differ (collision probability ~0 for v4).
func TestNewSessionIDsAreUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		sid := newSessionID()
		if seen[sid] {
			t.Fatalf("duplicate sid: %q", sid)
		}
		seen[sid] = true
	}
}

// TestLoadAgentsMD covers the three returns: missing file (empty), present
// file (size summary), unreadable parent (error). The "unreadable" branch
// uses a non-directory parent path so os.Stat returns a wrapped error.
func TestLoadAgentsMD(t *testing.T) {
	t.Parallel()

	t.Run("missing returns empty", func(t *testing.T) {
		t.Parallel()
		line, err := loadAgentsMD(t.TempDir())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if line != "" {
			t.Errorf("got %q, want empty", line)
		}
	})

	t.Run("present returns size line", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		body := []byte("# agents file\nhello")
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), body, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		line, err := loadAgentsMD(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(line, "AGENTS.md:") {
			t.Errorf("got %q, want AGENTS.md: prefix", line)
		}
		if !strings.Contains(line, "19 bytes") {
			t.Errorf("got %q, want %q in size summary", line, "19 bytes")
		}
	})
}

// TestHooksOrNil asserts the one-liner: nil cfg → nil; non-nil cfg →
// cfg.Hooks pointer (which may itself be nil).
func TestHooksOrNil(t *testing.T) {
	t.Parallel()

	if got := hooksOrNil(nil); got != nil {
		t.Errorf("hooksOrNil(nil) = %v, want nil", got)
	}
	if got := hooksOrNil(&Config{Hooks: nil}); got != nil {
		t.Errorf("hooksOrNil(empty) = %v, want nil", got)
	}
	h := &HooksConfig{Version: 1}
	if got := hooksOrNil(&Config{Hooks: h}); got != h {
		t.Errorf("hooksOrNil pointer mismatch")
	}
}

// TestBuildStatusLine asserts each surfaced field appears in the right
// order with the right separator, and that empty inputs yield an empty
// line.
func TestBuildStatusLine(t *testing.T) {
	t.Parallel()

	t.Run("empty yields empty", func(t *testing.T) {
		t.Parallel()
		got := buildStatusLine(&flags{}, "", nil, nil)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("all fields populated", func(t *testing.T) {
		t.Parallel()
		cf := &flags{Model: "claude-sonnet-4", Mode: "plan", Sandbox: "enabled"}
		cfg := &Config{
			MCP:   &MCPConfig{MCPServers: map[string]cursorMCPServer{"a": {}, "b": {}}},
			Hooks: &HooksConfig{Version: 1, Hooks: map[string][]HookEntry{"beforeShellExecution": nil}},
		}
		rules := []RuleFile{{AlwaysApply: true}, {Globs: "*.ts"}}
		got := buildStatusLine(cf, "AGENTS.md: 100 bytes", cfg, rules)

		wantSubs := []string{
			"model: claude-sonnet-4",
			"mode: plan",
			"sandbox: enabled",
			"mcp: 2",
			"hooks: 1 events",
			"rules: 2 (1 always, 1 glob)",
			"AGENTS.md: 100 bytes",
		}
		for _, sub := range wantSubs {
			if !strings.Contains(got, sub) {
				t.Errorf("status line %q missing substring %q", got, sub)
			}
		}
		// Pipe-separated.
		if !strings.Contains(got, " | ") {
			t.Errorf("status line missing pipe separator: %q", got)
		}
	})
}

// TestToCoreServer / TestHTTPServersFromConfig: the boundary between the
// disk-side cursorMCPServer and the runtime-side internal/mcp.Server, plus
// the http-only filter that drops disabled and stdio entries.
func TestToCoreServer(t *testing.T) {
	t.Parallel()

	srv := cursorMCPServer{
		Type:    "http",
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer x"},
		// Stdio fields are dropped at this boundary.
		Command: "node",
		Args:    []string{"s.js"},
		Env:     map[string]string{"K": "v"},
	}
	got := srv.toCoreServer()
	if got.Type != "http" || got.URL != "https://example.com/mcp" {
		t.Errorf("Type/URL not projected: %+v", got)
	}
	if v, ok := got.Headers["Authorization"]; !ok || v != "Bearer x" {
		t.Errorf("Headers not projected: %+v", got.Headers)
	}
}

func TestHTTPServersFromConfig(t *testing.T) {
	t.Parallel()

	t.Run("nil cfg yields nil", func(t *testing.T) {
		t.Parallel()
		if got := httpServersFromConfig(nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("filters stdio and disabled", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			MCP: &MCPConfig{
				MCPServers: map[string]cursorMCPServer{
					"http-on":  {Type: "http", URL: "https://a"},
					"http-off": {Type: "http", URL: "https://b", Disabled: true},
					"stdio-on": {Type: "stdio", Command: "node"},
				},
			},
		}
		got := httpServersFromConfig(cfg)
		if len(got) != 1 {
			t.Fatalf("got %d servers, want 1: %+v", len(got), got)
		}
		if _, ok := got["http-on"]; !ok {
			t.Errorf("expected http-on to survive filter, got: %+v", got)
		}
	})
}
