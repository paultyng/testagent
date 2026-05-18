package cursor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paultyng/testagent/internal/rootflags"
)

// writeMCPJSON is a test helper that writes a minimal mcp.json under
// <dir>/.cursor/mcp.json so loadConfig finds it as the project config.
func writeMCPJSON(t *testing.T, dir string, content []byte) {
	t.Helper()
	cursorDir := filepath.Join(dir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatalf("mkdir .cursor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "mcp.json"), content, 0o644); err != nil {
		t.Fatalf("writing mcp.json: %v", err)
	}
}

// runMCPCmd wires a fresh NewCommand rooted at the cursor package, sets args,
// captures stdout+stderr into a single buffer, and returns the output and any
// error. The working directory is changed to wsDir before execution.
func runMCPCmd(t *testing.T, wsDir string, args ...string) (string, error) {
	t.Helper()

	// Redirect cwd for loadConfig so it reads from wsDir.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(wsDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	var buf bytes.Buffer
	cmd := NewCommand(&rootflags.Flags{})
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	execErr := cmd.Execute()
	return buf.String(), execErr
}

// TestMCPList_OutputShape verifies that mcp list emits one line per server
// containing the correct name, status, and transport tokens.
func TestMCPList_OutputShape(t *testing.T) {
	// Uses t.Setenv indirectly (CURSOR_HOME must be set to avoid reading the
	// real home dir). Cannot use t.Parallel().
	wsDir := t.TempDir()
	homeDir := t.TempDir()

	// Empty user-level config so we don't inherit real ~/.cursor/mcp.json.
	t.Setenv("CURSOR_HOME", homeDir)

	fixture := []byte(`{
		"mcpServers": {
			"http-server": {"type": "http", "url": "https://example.com/mcp"},
			"another-http": {"type": "http", "url": "https://other.example.com/mcp"},
			"stdio-server": {"type": "stdio", "command": "node", "args": ["s.js"], "disabled": true}
		}
	}`)
	writeMCPJSON(t, wsDir, fixture)

	out, err := runMCPCmd(t, wsDir, "mcp", "list")
	if err != nil {
		t.Fatalf("mcp list: unexpected error: %v", err)
	}

	tests := []struct {
		server    string
		status    string
		transport string
	}{
		{"http-server", "enabled", "http"},
		{"another-http", "enabled", "http"},
		{"stdio-server", "disabled", "stdio"},
	}
	for _, tc := range tests {
		if !strings.Contains(out, tc.server) {
			t.Errorf("output missing server %q", tc.server)
		}
		// Find the line for this server and check it contains status and transport.
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, tc.server+"\t") {
				if !strings.Contains(line, tc.status) {
					t.Errorf("server %q line %q: want status %q", tc.server, line, tc.status)
				}
				if !strings.Contains(line, tc.transport) {
					t.Errorf("server %q line %q: want transport %q", tc.server, line, tc.transport)
				}
			}
		}
	}
}

// TestMCPListTools_UnknownServer asserts an error when the named server is
// absent from all config sources.
func TestMCPListTools_UnknownServer(t *testing.T) {
	wsDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("CURSOR_HOME", homeDir)

	writeMCPJSON(t, wsDir, []byte(`{"mcpServers":{"real-server":{"type":"http","url":"https://example.com"}}}`))

	_, err := runMCPCmd(t, wsDir, "mcp", "list-tools", "ghost-server")
	if err == nil {
		t.Fatal("expected error for unknown server, got nil")
	}
}

// TestMCPListTools_DisabledServer asserts an error when the named server
// exists but has disabled:true.
func TestMCPListTools_DisabledServer(t *testing.T) {
	wsDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("CURSOR_HOME", homeDir)

	writeMCPJSON(t, wsDir, []byte(`{"mcpServers":{"off-server":{"type":"http","url":"https://example.com","disabled":true}}}`))

	_, err := runMCPCmd(t, wsDir, "mcp", "list-tools", "off-server")
	if err == nil {
		t.Fatal("expected error for disabled server, got nil")
	}
}

// TestMCPListTools_Stdio asserts the not-yet-supported error for stdio servers.
func TestMCPListTools_Stdio(t *testing.T) {
	wsDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("CURSOR_HOME", homeDir)

	writeMCPJSON(t, wsDir, []byte(`{"mcpServers":{"stdio-srv":{"type":"stdio","command":"node","args":["s.js"]}}}`))

	_, err := runMCPCmd(t, wsDir, "mcp", "list-tools", "stdio-srv")
	if err == nil {
		t.Fatal("expected error for stdio server, got nil")
	}
	if !strings.Contains(err.Error(), "stdio") {
		t.Errorf("error %q: want mention of \"stdio\"", err.Error())
	}
}

// TestMCPEnableDisable_RoundTrip writes a user mcp.json with one enabled server
// plus an extra top-level key, disables the server, re-reads, asserts
// disabled:true, then enables it, re-reads, asserts disabled absent.
// Unknown sibling keys must survive both round-trips.
// Cannot use t.Parallel() because t.Setenv is used.
func TestMCPEnableDisable_RoundTrip(t *testing.T) {
	cursorHome := t.TempDir()
	t.Setenv("CURSOR_HOME", cursorHome)

	userCursorDir := filepath.Join(cursorHome, ".cursor")
	if err := os.MkdirAll(userCursorDir, 0o755); err != nil {
		t.Fatalf("mkdir user .cursor: %v", err)
	}
	mcpPath := filepath.Join(userCursorDir, "mcp.json")

	initial := []byte(`{
		"comments": "preserve me",
		"mcpServers": {
			"my-server": {"type": "http", "url": "https://example.com/mcp"}
		}
	}`)
	if err := os.WriteFile(mcpPath, initial, 0o600); err != nil {
		t.Fatalf("writing mcp.json: %v", err)
	}

	wsDir := t.TempDir()

	// --- disable ---
	_, err := runMCPCmd(t, wsDir, "mcp", "disable", "my-server")
	if err != nil {
		t.Fatalf("mcp disable: unexpected error: %v", err)
	}

	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("reading mcp.json after disable: %v", err)
	}
	var afterDisable map[string]any
	if err := json.Unmarshal(data, &afterDisable); err != nil {
		t.Fatalf("parsing mcp.json after disable: %v", err)
	}
	assertDisabledFlag(t, afterDisable, "my-server", true)
	assertPreservedKey(t, afterDisable, "comments", "preserve me")

	// --- enable ---
	_, err = runMCPCmd(t, wsDir, "mcp", "enable", "my-server")
	if err != nil {
		t.Fatalf("mcp enable: unexpected error: %v", err)
	}

	data, err = os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("reading mcp.json after enable: %v", err)
	}
	var afterEnable map[string]any
	if err := json.Unmarshal(data, &afterEnable); err != nil {
		t.Fatalf("parsing mcp.json after enable: %v", err)
	}
	assertDisabledFlag(t, afterEnable, "my-server", false)
	assertPreservedKey(t, afterEnable, "comments", "preserve me")
}

// assertDisabledFlag checks the disabled field in mcpServers.<name>.
// When want is false it asserts the key is absent (enable removes it).
func assertDisabledFlag(t *testing.T, raw map[string]any, serverName string, want bool) {
	t.Helper()
	servers, ok := raw["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or not an object")
	}
	entry, ok := servers[serverName].(map[string]any)
	if !ok {
		t.Fatalf("server %q missing or not an object", serverName)
	}
	got, present := entry["disabled"]
	if want {
		if !present {
			t.Errorf("server %q: want disabled=true, key absent", serverName)
			return
		}
		if v, _ := got.(bool); !v {
			t.Errorf("server %q: disabled = %v, want true", serverName, got)
		}
	} else {
		if present {
			t.Errorf("server %q: want disabled absent after enable, got %v", serverName, got)
		}
	}
}

// assertPreservedKey checks that a top-level key retains its expected value.
func assertPreservedKey(t *testing.T, raw map[string]any, key, want string) {
	t.Helper()
	v, ok := raw[key]
	if !ok {
		t.Errorf("top-level key %q missing", key)
		return
	}
	if s, _ := v.(string); s != want {
		t.Errorf("key %q = %q, want %q", key, s, want)
	}
}
