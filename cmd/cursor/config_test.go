package cursor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// testdataPath returns the absolute path to a file under testdata/.
func testdataPath(t *testing.T, elem ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	base := filepath.Join(filepath.Dir(file), "testdata")
	return filepath.Join(append([]string{base}, elem...)...)
}

func TestLoadMCPConfig(t *testing.T) {
	t.Parallel()

	t.Run("valid two-server fixture", func(t *testing.T) {
		t.Parallel()
		got, err := loadMCPConfig(testdataPath(t, "mcp.json"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil MCPConfig")
		}
		if len(got.MCPServers) != 2 {
			t.Fatalf("got %d servers, want 2", len(got.MCPServers))
		}

		stdio, ok := got.MCPServers["file-server"]
		if !ok {
			t.Fatal("missing server \"file-server\"")
		}
		if stdio.Type != "stdio" {
			t.Errorf("file-server.Type = %q, want \"stdio\"", stdio.Type)
		}

		remote, ok := got.MCPServers["remote-api"]
		if !ok {
			t.Fatal("missing server \"remote-api\"")
		}
		if remote.Type != "http" {
			t.Errorf("remote-api.Type = %q, want \"http\"", remote.Type)
		}
		if remote.URL != "https://example.com/mcp" {
			t.Errorf("remote-api.URL = %q, want https://example.com/mcp", remote.URL)
		}
		if remote.Headers["Authorization"] != "Bearer token123" {
			t.Errorf("remote-api Authorization header = %q, want \"Bearer token123\"", remote.Headers["Authorization"])
		}
	})

	t.Run("missing file returns nil nil", func(t *testing.T) {
		t.Parallel()
		got, err := loadMCPConfig(testdataPath(t, "nonexistent.json"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil MCPConfig, got %+v", got)
		}
	})

	t.Run("malformed json returns error", func(t *testing.T) {
		t.Parallel()
		got, err := loadMCPConfig(testdataPath(t, "mcp-malformed.json"))
		if err == nil {
			t.Fatalf("expected error, got nil (config: %+v)", got)
		}
	})
}

func TestLoadHooksConfig(t *testing.T) {
	t.Parallel()

	t.Run("valid full-feature fixture", func(t *testing.T) {
		t.Parallel()
		got, err := loadHooksConfig(testdataPath(t, "hooks.json"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil HooksConfig")
		}
		if got.Version != 1 {
			t.Errorf("Version = %d, want 1", got.Version)
		}

		// beforeShellExecution: has matcher and failClosed
		bse := got.Hooks["beforeShellExecution"]
		if len(bse) != 1 {
			t.Fatalf("beforeShellExecution: got %d entries, want 1", len(bse))
		}
		if bse[0].Matcher != "rm -rf" {
			t.Errorf("beforeShellExecution[0].Matcher = %q, want \"rm -rf\"", bse[0].Matcher)
		}
		if !bse[0].FailClosed {
			t.Error("beforeShellExecution[0].FailClosed should be true")
		}
		if bse[0].Timeout != 30 {
			t.Errorf("beforeShellExecution[0].Timeout = %d, want 30", bse[0].Timeout)
		}

		// afterFileEdit: type "prompt"
		afe := got.Hooks["afterFileEdit"]
		if len(afe) != 1 {
			t.Fatalf("afterFileEdit: got %d entries, want 1", len(afe))
		}
		if afe[0].Type != "prompt" {
			t.Errorf("afterFileEdit[0].Type = %q, want \"prompt\"", afe[0].Type)
		}

		// beforeMCPExecution: loop_limit is JSON null → pointer is nil
		bme := got.Hooks["beforeMCPExecution"]
		if len(bme) != 1 {
			t.Fatalf("beforeMCPExecution: got %d entries, want 1", len(bme))
		}
		if bme[0].LoopLimit != nil {
			t.Errorf("beforeMCPExecution[0].LoopLimit = %v, want nil (JSON null)", bme[0].LoopLimit)
		}

		// subagentStart: explicit loop_limit value
		ss := got.Hooks["subagentStart"]
		if len(ss) != 1 {
			t.Fatalf("subagentStart: got %d entries, want 1", len(ss))
		}
		if ss[0].LoopLimit == nil {
			t.Fatal("subagentStart[0].LoopLimit should not be nil")
		}
		if *ss[0].LoopLimit != 3 {
			t.Errorf("subagentStart[0].LoopLimit = %d, want 3", *ss[0].LoopLimit)
		}

		// beforeReadFile: minimal entry
		brf := got.Hooks["beforeReadFile"]
		if len(brf) != 1 {
			t.Fatalf("beforeReadFile: got %d entries, want 1", len(brf))
		}
		if brf[0].Command != "redact.sh" {
			t.Errorf("beforeReadFile[0].Command = %q, want \"redact.sh\"", brf[0].Command)
		}
	})

	t.Run("missing file returns nil nil", func(t *testing.T) {
		t.Parallel()
		got, err := loadHooksConfig(testdataPath(t, "nonexistent-hooks.json"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil HooksConfig, got %+v", got)
		}
	})

	t.Run("bad version returns error", func(t *testing.T) {
		t.Parallel()
		_, err := loadHooksConfig(testdataPath(t, "hooks-bad-version.json"))
		if err == nil {
			t.Fatal("expected error for version 2, got nil")
		}
	})

	t.Run("missing version returns error", func(t *testing.T) {
		t.Parallel()
		_, err := loadHooksConfig(testdataPath(t, "hooks-missing-version.json"))
		if err == nil {
			t.Fatal("expected error for missing version, got nil")
		}
	})
}

func TestLoadConfigPrecedence(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel(); run sequentially.

	// Build a temp workspace with .cursor/mcp.json from testdata/mcp.json.
	// CURSOR_HOME is set to testdata/home, which contains .cursor/mcp.json
	// declaring "file-server" (http, global URL) and "global-only".
	// The project declares "file-server" (stdio) and "remote-api".
	// Project value must WIN for "file-server"; "global-only" must survive.

	wsDir := t.TempDir()
	cursorDir := filepath.Join(wsDir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatalf("mkdir .cursor: %v", err)
	}

	projectMCP, err := os.ReadFile(testdataPath(t, "mcp.json"))
	if err != nil {
		t.Fatalf("reading testdata/mcp.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "mcp.json"), projectMCP, 0o644); err != nil {
		t.Fatalf("writing workspace mcp.json: %v", err)
	}

	t.Setenv("CURSOR_HOME", testdataPath(t, "home"))

	cfg, err := loadConfig(wsDir)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg == nil || cfg.MCP == nil {
		t.Fatal("expected non-nil Config with MCP")
	}

	// Project value wins for "file-server".
	fs, ok := cfg.MCP.MCPServers["file-server"]
	if !ok {
		t.Fatal("missing server \"file-server\" in merged config")
	}
	if fs.Type != "stdio" {
		t.Errorf("file-server.Type = %q after merge, want \"stdio\" (project should win)", fs.Type)
	}

	// Global-only server must still appear.
	if _, ok := cfg.MCP.MCPServers["global-only"]; !ok {
		t.Error("missing server \"global-only\" — global servers should be included when not overridden")
	}

	// Project-only server must appear.
	if _, ok := cfg.MCP.MCPServers["remote-api"]; !ok {
		t.Error("missing server \"remote-api\" — project-only servers must be present")
	}
}
