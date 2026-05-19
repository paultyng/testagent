// End-to-end test: spawns @modelcontextprotocol/server-everything (the
// canonical reference MCP server) via npx as a stdio subprocess, has
// testagent connect through internal/mcp's stdio transport, runs the
// initialize + tools/list handshake, and asserts the tool set comes
// back. Exercises the full stdio path (process-group, env inheritance,
// stderr passthrough, Close subprocess kill) that the per-package tests
// cover in isolation, but against a real npm-distributed server so we
// catch ecosystem drift.
//
// Skips when:
//   - TESTAGENT_E2E_NPM_SKIP is set (opt-out for local fast iterations)
//   - npx is not on PATH (no Node.js installed)
//   - testing.Short is set (matches the existing e2e tests in this file)
//
// First run is slow (~10-30s) because npx downloads the package; later
// runs hit the npm cache and complete in ~2s.

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stdioServerEverythingArgs returns the npx invocation for the
// reference MCP server. Pinned by package name; npx resolves the
// version from the registry at run time.
func stdioServerEverythingArgs() (string, []string) {
	return "npx", []string{"--yes", "--", "@modelcontextprotocol/server-everything"}
}

// skipIfNoNpx skips when the e2e prerequisites aren't met.
func skipIfNoNpx(t *testing.T) {
	t.Helper()
	if os.Getenv("TESTAGENT_E2E_NPM_SKIP") != "" {
		t.Skip("TESTAGENT_E2E_NPM_SKIP set; npx-backed e2e skipped")
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not on PATH; install Node.js to run this test")
	}
}

func TestE2E_Cursor_MCPListTools_StdioServerEverything(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipped in -short mode")
	}
	skipIfNoNpx(t)
	t.Parallel()

	bin := buildTestAgent(t)

	// Write a workspace .cursor/mcp.json pointing at the reference
	// server. Using --workspace keeps the test from touching the user's
	// real ~/.cursor/mcp.json.
	ws := t.TempDir()
	cursorDir := filepath.Join(ws, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatalf("mkdir .cursor: %v", err)
	}
	cmdName, cmdArgs := stdioServerEverythingArgs()
	mcpJSON := map[string]any{
		"mcpServers": map[string]any{
			"everything": map[string]any{
				"type":    "stdio",
				"command": cmdName,
				"args":    cmdArgs,
			},
		},
	}
	mcpJSONBytes, err := json.MarshalIndent(mcpJSON, "", "  ")
	if err != nil {
		t.Fatalf("marshal mcp.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "mcp.json"), mcpJSONBytes, 0o600); err != nil {
		t.Fatalf("write mcp.json: %v", err)
	}

	// Override CURSOR_HOME so the user's real config isn't read.
	homeOverride := t.TempDir()

	// Generous timeout: first run downloads the package (~10-30s on a
	// cold runner); cached runs complete in ~2s.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--workspace", ws,
		"cursor", "mcp", "list-tools", "everything",
	)
	cmd.Env = append(os.Environ(), "CURSOR_HOME="+homeOverride)
	stdout, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if asExitError(err, &ee) {
			t.Fatalf("testagent cursor mcp list-tools everything: exit %d\nstdout: %s\nstderr: %s",
				ee.ExitCode(), stdout, ee.Stderr)
		}
		t.Fatalf("testagent cursor mcp list-tools everything: %v\nstdout: %s", err, stdout)
	}

	// server-everything ships a small but stable tool catalog. Don't
	// pin specific tool names (the server's surface evolves with mcp
	// upstream); assert the output shape:
	//   - non-empty
	//   - at least 3 lines (server-everything has 9+ tools today; pad
	//     the floor so a future trim doesn't drop us to zero silently)
	//   - each line matches the documented "<server.tool>\t<description>"
	//     format from cmd/cursor/mcp.go's list-tools handler
	out := strings.TrimRight(string(stdout), "\n")
	if out == "" {
		t.Fatalf("mcp list-tools produced no output")
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Errorf("got %d tool lines, want >= 3 (server-everything advertises more)\n--- stdout ---\n%s", len(lines), stdout)
	}
	prefix := "everything."
	for i, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			t.Errorf("line %d %q does not start with %q", i, line, prefix)
			continue
		}
		if !strings.Contains(line, "\t") {
			t.Errorf("line %d %q missing tab separator (qualified-name\\tdescription)", i, line)
		}
	}
}
