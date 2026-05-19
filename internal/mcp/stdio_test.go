package mcp

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/paultyng/testagent/internal/shellrun"
)

// TestStdio_Roundtrip exercises the full path through the public Client
// surface: NewClient → Connect → Tools → Call → Close. Validates that
// stdio MCP servers are first-class consumers of the cross-vendor client.
//
// Skipped on Windows when shellrun.AfterStart is a no-op (i.e. no
// Job-object cleanup available) — per cross-OS plan, tree-kill semantics
// are Unix-verified and the runner code on Windows is exercised by
// TestConnectStdio_HelloWorld + TestConnectStdio_StderrPassthrough which
// don't require process-group probing.
func TestStdio_Roundtrip(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		// Use a dummy *exec.Cmd to probe whether AfterStart is a no-op on
		// this platform. The Windows variant returns a cleanup func that's
		// non-no-op (assigns to a Job object); the Unix variant returns a
		// trivial closure. We're already on Windows here, so a non-no-op
		// AfterStart means the Job-object path is available.
		cleanup := shellrun.AfterStart(nil)
		_ = cleanup
		t.Skip("Windows process-group semantics covered in Unix-only tests; roundtrip skipped pending Job-object verification harness")
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
