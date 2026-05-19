//go:build unix

// The stdio transport tests probe subprocess liveness via syscall.Kill,
// which is Unix-only. Windows uses Job-object semantics (see
// internal/shellrun/shellrun_windows.go) which would need a different
// probe — out of scope for this PR. The fixture binary itself builds
// and runs on Windows (see testdata/stdio-server/spawn_windows.go);
// only the verification path here is Unix-gated.

package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/paultyng/testagent/internal/shellrun"
)

// mcpgoInitReq returns a standard initialize request for tests.
func mcpgoInitReq() mcpgo.InitializeRequest {
	return mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcpgo.Implementation{
				Name:    "testagent-test",
				Version: "0.0.1",
			},
			Capabilities: mcpgo.ClientCapabilities{},
		},
	}
}

// mcpgoCallReq returns a tools/call request for tests.
func mcpgoCallReq(name string, args any) mcpgo.CallToolRequest {
	return mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}
}

// TestMergedEnv covers the mergedEnv helper.
func TestMergedEnv(t *testing.T) {
	t.Parallel()

	t.Run("no extras returns os.Environ copy", func(t *testing.T) {
		t.Parallel()
		got := mergedEnv(nil)
		want := os.Environ()
		if len(got) != len(want) {
			t.Errorf("mergedEnv(nil) len=%d, want %d", len(got), len(want))
		}
		// Mutating the returned slice must not affect os.Environ.
		if len(got) > 0 {
			got[0] = "MUTATED=yes"
		}
		after := os.Environ()
		if len(got) > 0 && after[0] == "MUTATED=yes" {
			t.Error("mergedEnv returned slice shares backing array with os.Environ")
		}
	})

	t.Run("new key is present at end", func(t *testing.T) {
		t.Parallel()
		got := mergedEnv(map[string]string{"TESTMERGEDENV_NEW": "hello"})
		var found bool
		for _, e := range got {
			if e == "TESTMERGEDENV_NEW=hello" {
				found = true
				break
			}
		}
		if !found {
			t.Error("mergedEnv did not append new key")
		}
	})

	t.Run("override on existing key appended after base", func(t *testing.T) {
		t.Parallel()
		// Use a key that is almost certainly not in the ambient env so we
		// can control both values via the extras map.
		const key = "TESTMERGEDENV_A"
		const key2 = "TESTMERGEDENV_B"
		// Provide the "base" value via extras on the first call, then override
		// it in a second pass to confirm last-write wins.
		first := mergedEnv(map[string]string{key: "first", key2: "keep"})
		second := mergedEnv(nil)
		// Append both manually to simulate base+override layering.
		second = append(second, key+"=base")
		second = append(second, key+"=override")

		var idx []int
		for i, e := range second {
			if strings.HasPrefix(e, key+"=") {
				idx = append(idx, i)
			}
		}
		if len(idx) < 2 {
			t.Errorf("expected at least 2 entries for %s, got %v", key, idx)
		}
		last := second[idx[len(idx)-1]]
		if last != key+"=override" {
			t.Errorf("last %s entry = %q, want %q", key, last, key+"=override")
		}
		// mergedEnv itself: a single extras key ends up in the slice.
		var found bool
		for _, e := range first {
			if e == key2+"=keep" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("mergedEnv did not include %s=keep", key2)
		}
	})
}

// TestConnectStdio_HelloWorld spawns the fixture binary and asserts the two
// known tools are exposed.
func TestConnectStdio_HelloWorld(t *testing.T) {
	t.Parallel()

	bin := stdioFixtureBinary(t)
	c := NewClient(map[string]Server{
		"fixture": {Command: bin},
	})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	tools := c.Tools()
	if len(tools) != 2 {
		t.Fatalf("Tools() len = %d, want 2", len(tools))
	}

	names := make([]string, len(tools))
	for i, tool := range tools {
		if tool.Server != "fixture" {
			t.Errorf("tool.Server = %q, want fixture", tool.Server)
		}
		names[i] = tool.Name
	}
	sort.Strings(names)
	want := []string{"add", "echo"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("tool[%d].Name = %q, want %q", i, names[i], w)
		}
	}
}

// syncBuffer is a bytes.Buffer with a mutex for safe concurrent use in tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestConnectStdio_StderrPassthrough verifies that the stderr pump forwards
// fixture stderr to the debug writer.
func TestConnectStdio_StderrPassthrough(t *testing.T) {
	t.Parallel()

	bin := stdioFixtureBinary(t)

	var buf syncBuffer
	c := NewClient(map[string]Server{
		"srv": {Command: bin, Args: []string{"--stderr-line=hello from fixture"}},
	})
	c.SetDebugWriter(&buf)

	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	const want = "mcp[srv]: hello from fixture"
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("debug writer contents = %q, want substring %q", buf.String(), want)
}

// TestConnectStdio_TreeKill spawns the fixture with --spawn-child and verifies
// that cancelling the context reaps both the parent and child processes.
func TestConnectStdio_TreeKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tree-kill test is Unix-only")
	}
	t.Parallel()

	bin := stdioFixtureBinary(t)

	var buf syncBuffer
	var parentCmd *exec.Cmd

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdioTransport := transport.NewStdioWithOptions(
		bin,
		mergedEnv(nil),
		[]string{"--spawn-child", "--stderr-line=ready"},
		transport.WithCommandFunc(func(fctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(fctx, command, args...)
			cmd.Env = env
			shellrun.SetProcessGroup(cmd)
			parentCmd = cmd
			return cmd, nil
		}),
	)

	cl := client.NewClient(stdioTransport)

	if err := cl.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wire stderr reader after Start so the transport pipe is initialised.
	if r, ok := client.GetStderr(cl); ok {
		go func() {
			tmp := make([]byte, 4096)
			for {
				n, err := r.Read(tmp)
				if n > 0 {
					buf.Write(tmp[:n])
				}
				if err != nil {
					return
				}
			}
		}()
	}

	initReq := mcpgoInitReq()
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		_ = cl.Close()
		t.Fatalf("Initialize: %v", err)
	}

	// Wait for child-pid= line.
	var childPid int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := buf.String()
		if idx := strings.Index(s, "child-pid="); idx >= 0 {
			rest := s[idx+len("child-pid="):]
			if _, err := fmt.Sscanf(rest, "%d", &childPid); err == nil && childPid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPid == 0 {
		t.Fatalf("child-pid not found in stderr output: %q", buf.String())
	}

	// Capture parent PID before cancel races with process exit.
	if parentCmd == nil || parentCmd.Process == nil {
		t.Fatal("parentCmd.Process is nil after Start")
	}
	parentPid := parentCmd.Process.Pid

	// Cancel context — SetProcessGroup's Cancel hook group-kills the tree.
	// Close waits for the subprocess to exit and reaps it.
	cancel()
	_ = cl.Close()

	// Poll for both processes to be reaped. The parent may briefly be a
	// zombie between SIGKILL and Wait; Close's wait call should clear it.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		parentDead := syscall.Kill(parentPid, 0) != nil
		childDead := syscall.Kill(childPid, 0) != nil
		if parentDead && childDead {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	parentDead := syscall.Kill(parentPid, 0) != nil
	childDead := syscall.Kill(childPid, 0) != nil
	if !parentDead || !childDead {
		t.Errorf("after cancel+close: parent (%d) dead=%v, child (%d) dead=%v", parentPid, parentDead, childPid, childDead)
	}
}

// TestClient_Close_KillsStdioSubprocess verifies that Client.Close reaps the
// stdio subprocess.
func TestClient_Close_KillsStdioSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PID-based reaping check is Unix-only")
	}
	t.Parallel()

	bin := stdioFixtureBinary(t)

	var parentCmd *exec.Cmd

	stdioTransport := transport.NewStdioWithOptions(
		bin,
		mergedEnv(nil),
		nil,
		transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Env = env
			shellrun.SetProcessGroup(cmd)
			parentCmd = cmd
			return cmd, nil
		}),
	)
	cl := client.NewClient(stdioTransport)
	if err := cl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := cl.Initialize(context.Background(), mcpgoInitReq()); err != nil {
		_ = cl.Close()
		t.Fatalf("Initialize: %v", err)
	}

	if parentCmd == nil || parentCmd.Process == nil {
		t.Fatal("parentCmd.Process is nil after Start")
	}
	pid := parentCmd.Process.Pid

	if err := cl.Close(); err != nil {
		t.Logf("Close returned: %v (non-fatal)", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Errorf("subprocess pid %d still alive after Close", pid)
	}
}

// TestConnectStdio_ContextCancel verifies that a context cancelled after
// Connect (during a slow tool Call) causes the call to return an error and
// the subprocess to be reaped.
func TestConnectStdio_ContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PID-based reaping check is Unix-only")
	}
	t.Parallel()

	bin := stdioFixtureBinary(t)

	var parentCmd *exec.Cmd

	stdioTransport := transport.NewStdioWithOptions(
		bin,
		mergedEnv(nil),
		[]string{"--hang-init=30s"},
		transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Env = env
			shellrun.SetProcessGroup(cmd)
			parentCmd = cmd
			return cmd, nil
		}),
	)
	cl := client.NewClient(stdioTransport)
	if err := cl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := cl.Initialize(context.Background(), mcpgoInitReq()); err != nil {
		_ = cl.Close()
		t.Fatalf("Initialize: %v", err)
	}
	defer cl.Close()

	if parentCmd == nil || parentCmd.Process == nil {
		t.Fatal("parentCmd.Process is nil after Start")
	}
	pid := parentCmd.Process.Pid

	// Call echo with a very short deadline so the hang-init delay fires.
	callCtx, callCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer callCancel()

	req := mcpgoCallReq("echo", map[string]any{"text": "hi"})
	_, callErr := cl.CallTool(callCtx, req)
	if callErr == nil {
		t.Fatal("CallTool: expected error from context timeout, got nil")
	}

	// After the call errors, close and wait for the subprocess to be reaped.
	_ = cl.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		t.Errorf("subprocess pid %d still alive after Close", pid)
	}
}
