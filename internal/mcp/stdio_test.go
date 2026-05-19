package mcp

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// TestStdio_Roundtrip exercises the full path through the public Client
// surface: NewClient → Connect → Tools → Call → Close. Validates that
// stdio MCP servers are first-class consumers of the cross-vendor client.
//
// Skipped on Windows: tree-kill verification depends on syscall.Kill
// (covered in Unix-tagged tests). Windows runner code is exercised by
// the e2e test in mcp_stdio_e2e_test.go via the cursor adapter's
// mcp list-tools subcommand, which goes through the same connectStdio
// path without needing a PID-liveness probe.
func TestStdio_Roundtrip(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("PID-liveness probing is Unix-only; cross-platform stdio coverage in mcp_stdio_e2e_test.go")
	}

	bin := stdioFixtureBinary(t)
	c := NewClient(map[string]Server{
		"fix": {Command: bin},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	// 1. Tools enumeration — assert both fixture tools appear.
	toolNames := make(map[string]bool, len(c.Tools()))
	for _, tool := range c.Tools() {
		toolNames[tool.Server+"."+tool.Name] = true
	}
	if !toolNames["fix.echo"] || !toolNames["fix.add"] {
		t.Fatalf("Tools() missing fix.echo or fix.add: got %v", toolNames)
	}

	// 2. Call(fix.echo) — string echo round-trip.
	echoRes, err := c.Call(ctx, "fix.echo", map[string]any{"text": "round-trip"})
	if err != nil {
		t.Fatalf("Call fix.echo: %v", err)
	}
	if echoRes.IsError {
		t.Fatalf("Call fix.echo returned IsError=true: %+v", echoRes)
	}
	if len(echoRes.Content) != 1 || echoRes.Content[0].Text != "round-trip" {
		t.Errorf("echo content = %+v, want one entry with text \"round-trip\"", echoRes.Content)
	}

	// 3. Call(fix.add) — numeric arg → text result.
	addRes, err := c.Call(ctx, "fix.add", map[string]any{"a": 2.5, "b": 1.5})
	if err != nil {
		t.Fatalf("Call fix.add: %v", err)
	}
	if addRes.IsError {
		t.Fatalf("Call fix.add returned IsError=true: %+v", addRes)
	}
	if len(addRes.Content) != 1 || !strings.HasPrefix(addRes.Content[0].Text, "4") {
		t.Errorf("add content = %+v, want one entry starting with \"4\"", addRes.Content)
	}
}
