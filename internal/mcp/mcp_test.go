package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeMCPServer is a minimal MCP-over-HTTP server for tests.
//
// It implements just enough of the streamable HTTP transport contract to
// drive the handshake and a tools/call:
//
//   - POST application/json with method "initialize"            → returns init result
//   - POST application/json with method "notifications/initialized" → 202
//   - POST application/json with method "tools/list"            → returns tools
//   - POST application/json with method "tools/call"            → returns content
//   - DELETE                                                    → 204 (close)
//
// useSSE controls whether responses are framed as text/event-stream.
type fakeMCPServer struct {
	name      string
	tools     []map[string]any
	useSSE    bool
	callReply func(toolName string, args map[string]any) (content []map[string]any, isError bool)

	mu          sync.Mutex
	requests    []recordedRequest
	sessionID   string
	initialized bool
}

type recordedRequest struct {
	Method  string
	Header  http.Header
	RPCBody map[string]any
}

func (f *fakeMCPServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var rpc map[string]any
		if err := json.Unmarshal(body, &rpc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		f.mu.Lock()
		f.requests = append(f.requests, recordedRequest{
			Method:  fmt.Sprint(rpc["method"]),
			Header:  r.Header.Clone(),
			RPCBody: rpc,
		})
		f.mu.Unlock()

		method, _ := rpc["method"].(string)
		switch method {
		case "initialize":
			// Assign a session ID on init.
			f.mu.Lock()
			f.sessionID = "session-" + f.name
			sid := f.sessionID
			f.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
			f.writeResult(w, rpc["id"], map[string]any{
				"protocolVersion": "2025-11-25",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":      map[string]any{"name": f.name, "version": "0.0.1"},
			})
		case "notifications/initialized":
			f.mu.Lock()
			f.initialized = true
			f.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			f.writeResult(w, rpc["id"], map[string]any{
				"tools": f.tools,
			})
		case "tools/call":
			params, _ := rpc["params"].(map[string]any)
			name, _ := params["name"].(string)
			args, _ := params["arguments"].(map[string]any)
			content := []map[string]any{{"type": "text", "text": fmt.Sprintf("called %s on %s", name, f.name)}}
			isErr := false
			if f.callReply != nil {
				content, isErr = f.callReply(name, args)
			}
			f.writeResult(w, rpc["id"], map[string]any{
				"content": content,
				"isError": isErr,
			})
		default:
			f.writeError(w, rpc["id"], -32601, "method not found: "+method)
		}
	}
}

func (f *fakeMCPServer) writeResult(w http.ResponseWriter, id any, result any) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
	f.writeFrame(w, resp)
}

func (f *fakeMCPServer) writeError(w http.ResponseWriter, id any, code int, msg string) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": msg},
	}
	f.writeFrame(w, resp)
}

func (f *fakeMCPServer) writeFrame(w http.ResponseWriter, frame map[string]any) {
	body, _ := json.Marshal(frame)
	if f.useSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		// SSE: data: <json>\n\n
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (f *fakeMCPServer) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// stdTools returns a small canned tool list with realistic shape.
func stdTools(prefix string) []map[string]any {
	return []map[string]any{
		{
			"name":        prefix + "_list",
			"description": "List " + prefix + " items",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{},
			},
		},
		{
			"name":        prefix + "_get",
			"description": "Get one " + prefix,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required": []string{"id"},
			},
		},
	}
}

func TestMCPClient_NilConfig(t *testing.T) {
	t.Parallel()

	c := NewClient(nil)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect on nil config: %v", err)
	}
	if got := c.Tools(); got != nil {
		t.Errorf("Tools() = %v, want nil", got)
	}
	if _, err := c.Call(context.Background(), "x.y", nil); !errors.Is(err, ErrNoServers) {
		t.Errorf("Call on nil config err = %v, want ErrNoServers", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// idempotent
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestMCPClient_EmptyConfig(t *testing.T) {
	t.Parallel()

	c := NewClient(map[string]Server{})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got := c.Tools(); got != nil {
		t.Errorf("Tools() = %v, want nil", got)
	}
	if _, err := c.Call(context.Background(), "x.y", nil); !errors.Is(err, ErrNoServers) {
		t.Errorf("Call err = %v, want ErrNoServers", err)
	}
	_ = c.Close()
}

func TestMCPClient_HandshakeAndTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		useSSE bool
	}{
		{"json", false},
		{"sse", true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeMCPServer{
				name:   "fileserver",
				tools:  stdTools("idea"),
				useSSE: tt.useSSE,
			}
			ts := httptest.NewServer(fake.handler())
			defer ts.Close()

			c := NewClient(map[string]Server{
				"fileserver": {Type: "http", URL: ts.URL},
			})
			if err := c.Connect(context.Background()); err != nil {
				t.Fatalf("Connect: %v", err)
			}
			defer c.Close()

			// Verify the handshake order on the wire.
			recs := fake.recorded()
			var methods []string
			for _, r := range recs {
				methods = append(methods, r.Method)
			}
			wantPrefix := []string{"initialize", "notifications/initialized", "tools/list"}
			if len(methods) < len(wantPrefix) {
				t.Fatalf("recorded methods = %v, want prefix %v", methods, wantPrefix)
			}
			for i, m := range wantPrefix {
				if methods[i] != m {
					t.Errorf("methods[%d] = %q, want %q", i, methods[i], m)
				}
			}
			if !fake.initialized {
				t.Errorf("server never received notifications/initialized")
			}

			// Verify tool list is qualified.
			tools := c.Tools()
			if len(tools) != 2 {
				t.Fatalf("Tools() len = %d, want 2", len(tools))
			}
			for _, tool := range tools {
				if tool.Server != "fileserver" {
					t.Errorf("tool.Server = %q, want fileserver", tool.Server)
				}
				if !strings.HasPrefix(tool.Name, "idea_") {
					t.Errorf("tool.Name = %q, want idea_ prefix", tool.Name)
				}
				if len(tool.InputSchema) == 0 {
					t.Errorf("tool %q has empty InputSchema", tool.Name)
				}
			}
		})
	}
}

func TestMCPClient_HeadersPropagated(t *testing.T) {
	t.Parallel()

	fake := &fakeMCPServer{name: "fileserver", tools: stdTools("idea")}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	c := NewClient(map[string]Server{
		"fileserver": {
			Type: "http",
			URL:  ts.URL,
			Headers: map[string]string{
				"Authorization": "Bearer test-token",
				"X-Session-Id":  "ses-from-config",
			},
		},
	})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	recs := fake.recorded()
	if len(recs) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, r := range recs {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token (method %s)", got, r.Method)
		}
		if got := r.Header.Get("X-Session-Id"); got != "ses-from-config" {
			t.Errorf("X-Session-Id = %q, want ses-from-config (method %s)", got, r.Method)
		}
	}
}

func TestMCPClient_UnionAcrossServers(t *testing.T) {
	t.Parallel()

	a := &fakeMCPServer{name: "alpha", tools: stdTools("alpha")}
	b := &fakeMCPServer{name: "beta", tools: stdTools("beta")}
	tsA := httptest.NewServer(a.handler())
	defer tsA.Close()
	tsB := httptest.NewServer(b.handler())
	defer tsB.Close()

	c := NewClient(map[string]Server{
		"alpha": {Type: "http", URL: tsA.URL},
		"beta":  {Type: "http", URL: tsB.URL},
	})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	tools := c.Tools()
	if len(tools) != 4 {
		t.Fatalf("Tools() len = %d, want 4", len(tools))
	}

	have := map[string]bool{}
	for _, tool := range tools {
		have[tool.Server+"."+tool.Name] = true
	}
	want := []string{"alpha.alpha_list", "alpha.alpha_get", "beta.beta_list", "beta.beta_get"}
	for _, q := range want {
		if !have[q] {
			t.Errorf("missing qualified tool %q in %v", q, have)
		}
	}
}

func TestMCPClient_Call(t *testing.T) {
	t.Parallel()

	fake := &fakeMCPServer{
		name:  "fileserver",
		tools: stdTools("idea"),
		callReply: func(name string, args map[string]any) ([]map[string]any, bool) {
			if name == "idea_get" {
				id, _ := args["id"].(string)
				return []map[string]any{{"type": "text", "text": "id=" + id}}, false
			}
			return nil, false
		},
	}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	c := NewClient(map[string]Server{
		"fileserver": {Type: "http", URL: ts.URL},
	})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	res, err := c.Call(context.Background(), "fileserver.idea_get", map[string]any{"id": "42"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Errorf("res.IsError = true, want false")
	}
	if len(res.Content) != 1 {
		t.Fatalf("res.Content len = %d, want 1", len(res.Content))
	}
	if res.Content[0].Type != "text" || res.Content[0].Text != "id=42" {
		t.Errorf("res.Content[0] = %+v, want {text id=42}", res.Content[0])
	}
}

func TestMCPClient_CallUnknown(t *testing.T) {
	t.Parallel()

	fake := &fakeMCPServer{name: "fileserver", tools: stdTools("idea")}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	c := NewClient(map[string]Server{
		"fileserver": {Type: "http", URL: ts.URL},
	})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	tests := []struct {
		name string
		qual string
	}{
		{"unknown server", "nope.idea_get"},
		{"unknown tool on known server", "fileserver.no_such_tool"},
		{"missing dot", "fileservertool"},
		{"empty server", ".idea_get"},
		{"empty tool", "fileserver."},
	}
	// Subtests are sequential here: they all share the same parent client,
	// and parallel subtests would race the parent's deferred Close.
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.Call(context.Background(), tt.qual, nil)
			if !errors.Is(err, ErrUnknownTool) {
				t.Errorf("err = %v, want ErrUnknownTool", err)
			}
		})
	}
}

func TestMCPClient_CloseIdempotent(t *testing.T) {
	t.Parallel()

	fake := &fakeMCPServer{name: "fileserver", tools: stdTools("idea")}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	c := NewClient(map[string]Server{
		"fileserver": {Type: "http", URL: ts.URL},
	})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Errorf("Close 1: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close 2 (idempotent): %v", err)
	}
	// Tools() after close returns nil (state cleared).
	if got := c.Tools(); got != nil {
		t.Errorf("Tools() after Close = %v, want nil", got)
	}
}

func TestMCPClient_ConnectIdempotent(t *testing.T) {
	t.Parallel()

	fake := &fakeMCPServer{name: "fileserver", tools: stdTools("idea")}
	ts := httptest.NewServer(fake.handler())
	defer ts.Close()

	c := NewClient(map[string]Server{
		"fileserver": {Type: "http", URL: ts.URL},
	})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect 1: %v", err)
	}
	defer c.Close()
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect 2: %v", err)
	}
	// Second Connect is a no-op (no extra requests beyond the first round).
	recs := fake.recorded()
	count := 0
	for _, r := range recs {
		if r.Method == "initialize" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("initialize observed %d times, want 1", count)
	}
}

func TestMCPClient_ConnectError(t *testing.T) {
	t.Parallel()

	// Server that 500s on the initialize request.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := NewClient(map[string]Server{
		"fileserver": {Type: "http", URL: ts.URL},
	})
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("Connect: want error, got nil")
	}
	if !strings.Contains(err.Error(), "fileserver") {
		t.Errorf("err = %v, want server name in message", err)
	}
	// Connect failure should leave no tools and Call should report unconfigured.
	if got := c.Tools(); got != nil {
		t.Errorf("Tools() after failed Connect = %v, want nil", got)
	}
	_ = c.Close()
}
