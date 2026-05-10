// End-to-end test: spawns the built testagent binary as a subprocess and
// drives it via stdin, verifying hook POSTs, MCP traffic, and rendered
// stdout. Exercises the integration of every v1 phase together.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// hookRecorder collects POST bodies from each hook URL path.
type hookRecorder struct {
	mu   sync.Mutex
	hits map[string][]map[string]any // keyed by path (e.g., "/hooks/prompt")
}

func newHookRecorder() *hookRecorder {
	return &hookRecorder{hits: make(map[string][]map[string]any)}
}

func (r *hookRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		r.hits[req.URL.Path] = append(r.hits[req.URL.Path], body)
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
}

func (r *hookRecorder) get(path string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, len(r.hits[path]))
	copy(out, r.hits[path])
	return out
}

// jsonRPCFakeServer fakes an MCP HTTP-streamable server: handshakes,
// returns one tool, and answers tools/call with text content.
func jsonRPCFakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusOK)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		method, _ := req["method"].(string)
		id := req["id"]
		w.Header().Set("Content-Type", "application/json")

		switch method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"protocolVersion": "2025-11-25",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "fake", "version": "0"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"tools": []map[string]any{
						{"name": "ping", "description": "returns pong", "inputSchema": map[string]any{"type": "object"}},
					},
				},
			})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "pong"}},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}))
}

// buildTestAgent compiles the testagent binary into a temp dir; returns its path.
func buildTestAgent(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "testagent")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// writeJSONFile writes a JSON value to a temp file under dir; returns path.
func writeJSONFile(t *testing.T, dir, name string, v any) string {
	t.Helper()
	path := filepath.Join(dir, name)
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestE2E_SlashFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipped in -short mode")
	}
	t.Parallel()

	bin := buildTestAgent(t)
	hr := newHookRecorder()
	hookSrv := httptest.NewServer(hr.handler())
	defer hookSrv.Close()
	mcpSrv := jsonRPCFakeServer(t)
	defer mcpSrv.Close()

	dir := t.TempDir()
	settingsPath := writeJSONFile(t, dir, "settings.json", map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []map[string]any{{"hooks": []map[string]any{{"type": "http", "url": hookSrv.URL + "/hooks/prompt", "timeout": 5}}}},
			"PostToolUse":      []map[string]any{{"hooks": []map[string]any{{"type": "http", "url": hookSrv.URL + "/hooks/tool-use", "timeout": 5}}}},
			"Stop":             []map[string]any{{"hooks": []map[string]any{{"type": "http", "url": hookSrv.URL + "/hooks/stop", "timeout": 5}}}},
			"SessionStart":     []map[string]any{{"hooks": []map[string]any{{"type": "http", "url": hookSrv.URL + "/hooks/start", "timeout": 5}}}},
			"SessionEnd":       []map[string]any{{"hooks": []map[string]any{{"type": "http", "url": hookSrv.URL + "/hooks/end", "timeout": 5}}}},
		},
	})
	mcpConfigPath := writeJSONFile(t, dir, "mcp.json", map[string]any{
		"mcpServers": map[string]any{
			"fake": map[string]any{"type": "http", "url": mcpSrv.URL},
		},
	})

	stdinScript := strings.Join([]string{
		`hi there`,                 // regular echo input → fires UserPromptSubmit + Stop
		`/think 1ms quick thought`, // /think 1ms hello — fires UserPromptSubmit + Stop via prompt-passthrough
		`/panel notable thing`,
		`/link https://example.com clickable-link`,
		`/fake-tool read_file {"path":"foo.go"}`,
		`/fake-tool-result {"contents":"package foo"}`,
		`/mcp-call fake.ping {}`,
		`/restart compact`, // fires SessionEnd{compact} + SessionStart{compact}
		`/exit`,
	}, "\n") + "\n"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"-n", "E2E",
		"--session-id", "e2e-session",
		"--think-delay", "5ms",
		"--settings", settingsPath,
		"--mcp-config", mcpConfigPath,
	)
	cmd.Stdin = strings.NewReader(stdinScript)
	stdout, err := cmd.Output()
	if err != nil {
		// /exit triggers a graceful shutdown(0); a non-zero exit is a regression.
		var ee *exec.ExitError
		if !asExitError(err, &ee) || ee.ExitCode() != 0 {
			t.Fatalf("testagent exited with err=%v\nstdout: %s", err, stdout)
		}
	}

	// Stdout assertions: each slash command produced its rendered output.
	wantInStdout := []string{
		"E2E",                               // banner name
		"hi there",                          // echo path
		"Thought for ",                      // post-thinking marker stays in scrollback
		"notable thing",                     // /panel content
		"\x1b]8;;https://example.com\x1b\\", // /link OSC 8 start sequence
		"clickable-link",                    // /link text
		"read_file",                         // /tool header
		`"path":"foo.go"`,                   // /tool args
		"package foo",                       // /result body
		"pong",                              // /mcp result content from fake server
	}
	out := string(stdout)
	for _, want := range wantInStdout {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\n--- full stdout ---\n%s", want, out)
		}
	}

	// Hook assertions:
	// - "hi there" raw input fires UserPromptSubmit + Stop
	// - "/think 1ms quick thought" routes through the prompt path → another
	//   UserPromptSubmit + Stop pair (proves the prompt-passthrough wiring)
	// - /fake-tool + /fake-tool-result fires PostToolUse
	// - /exit fires SessionEnd
	prompts := hr.get("/hooks/prompt")
	if len(prompts) != 2 {
		t.Errorf("UserPromptSubmit count = %d, want 2", len(prompts))
	} else {
		if prompts[0]["prompt"] != "hi there" {
			t.Errorf("first prompt = %v, want \"hi there\"", prompts[0]["prompt"])
		}
		if prompts[1]["prompt"] != "quick thought" {
			t.Errorf("second prompt (from /think) = %v, want \"quick thought\"", prompts[1]["prompt"])
		}
	}
	if got := hr.get("/hooks/tool-use"); len(got) != 1 {
		t.Errorf("PostToolUse count = %d, want 1", len(got))
	} else {
		if got[0]["tool_name"] != "read_file" {
			t.Errorf("tool_name = %v, want read_file", got[0]["tool_name"])
		}
		if resp, _ := got[0]["tool_response"].(map[string]any); resp == nil || resp["contents"] != "package foo" {
			t.Errorf("tool_response = %v, want {contents:package foo}", got[0]["tool_response"])
		}
		if dur, _ := got[0]["duration_ms"].(float64); dur < 0 {
			t.Errorf("duration_ms = %v, want >= 0", got[0]["duration_ms"])
		}
	}
	if got := hr.get("/hooks/stop"); len(got) != 2 {
		t.Errorf("Stop count = %d, want 2 (raw input + /think)", len(got))
	}

	// SessionStart fires on boot (source=startup, since --resume was not set)
	// and once more on /restart (source=compact).
	if got := hr.get("/hooks/start"); len(got) != 2 {
		t.Errorf("SessionStart count = %d, want 2 (boot + /restart)", len(got))
	} else {
		if got[0]["source"] != "startup" {
			t.Errorf("first SessionStart source = %v, want startup", got[0]["source"])
		}
		if got[1]["source"] != "compact" {
			t.Errorf("second SessionStart source = %v, want compact", got[1]["source"])
		}
	}

	// SessionEnd fires on /restart (reason=compact) and on /exit (reason=logout).
	if got := hr.get("/hooks/end"); len(got) != 2 {
		t.Errorf("SessionEnd count = %d, want 2 (/restart + /exit)", len(got))
	} else {
		if got[0]["reason"] != "compact" {
			t.Errorf("first SessionEnd reason = %v, want compact", got[0]["reason"])
		}
		if got[1]["reason"] != "logout" {
			t.Errorf("second SessionEnd reason = %v, want logout", got[1]["reason"])
		}
	}
}

func TestE2E_PrintStreamJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipped in -short mode")
	}
	t.Parallel()

	bin := buildTestAgent(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"-n", "PrintE2E",
		"--print",
		"--output-format", "stream-json",
		"--session-id", "e2e-print",
		"summarize the diff",
	)
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("testagent --print exited with err=%v\nstdout: %s", err, stdout)
	}

	lines := strings.Split(strings.TrimRight(string(stdout), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d stream-json lines, want 3\n%s", len(lines), stdout)
	}
	for i, expectedType := range []string{"system", "assistant", "result"} {
		var frame map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &frame); err != nil {
			t.Errorf("line %d not valid JSON: %v", i+1, err)
			continue
		}
		if frame["type"] != expectedType {
			t.Errorf("line %d type = %v, want %s", i+1, frame["type"], expectedType)
		}
	}
}

// asExitError extracts an *exec.ExitError, returning whether the error was one.
func asExitError(err error, target **exec.ExitError) bool {
	for e := err; e != nil; {
		if ee, ok := e.(*exec.ExitError); ok {
			*target = ee
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
