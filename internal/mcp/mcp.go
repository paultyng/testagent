// Package mcp provides an HTTP MCP client. Connects to servers declared
// by the caller, performs the standard MCP handshake (initialize +
// notifications/initialized + tools/list), and exposes a method to invoke
// tools. Built on github.com/mark3labs/mcp-go.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/paultyng/testagent/internal/shellrun"
)

// ErrNoServers is returned by Call when no MCP servers are configured.
var ErrNoServers = errors.New("no MCP servers configured")

// ErrUnknownTool is returned by Call when the qualified tool name does not
// resolve to a known server+tool.
var ErrUnknownTool = errors.New("unknown tool")

// Server is the on-disk shape of one MCP server entry. Field set covers
// both HTTP (URL + Headers) and stdio (Command + Args + Env) transports.
// resolveType(Server) returns the effective transport:
//   - explicit Type wins ("http" / "sse" / "stdio");
//   - empty Type infers stdio when Command is set, HTTP when URL is set;
//   - empty Type with neither set is an error at connect time.
type Server struct {
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// Command + Args spawn a stdio MCP subprocess. Ignored for http/sse.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// Env extras layered on top of the parent process env (last-write
	// wins). The subprocess sees the parent's full env including
	// ambient credentials — configure MCP servers you trust.
	Env map[string]string `json:"env,omitempty"`
}

// resolveType returns the effective transport type for a Server: "http",
// "stdio", or "" (unresolvable). Explicit Type wins (lowercased); otherwise
// Command implies stdio, URL implies http.
func resolveType(s Server) string {
	if s.Type != "" {
		return strings.ToLower(s.Type)
	}
	if s.Command != "" {
		return "stdio"
	}
	if s.URL != "" {
		return "http"
	}
	return ""
}

// Client connects to MCP servers and exposes the union of their tools.
type Client struct {
	config map[string]Server

	// debugWriter, when non-nil, receives per-server stderr prefixed with
	// "mcp[<name>]: ". Set via SetDebugWriter. Nil silences passthrough.
	debugWriter io.Writer

	mu        sync.Mutex
	servers   map[string]*serverConn // keyed by server name
	tools     []Tool                 // qualified, sorted by Server.Name
	connected bool
	closed    bool
}

// serverConn holds the per-server client + its tool list.
type serverConn struct {
	name   string
	client *client.Client
	tools  []mcpgo.Tool
}

// Tool is the union-of-tools view returned by Tools().
type Tool struct {
	Server      string
	Name        string
	Description string
	InputSchema json.RawMessage
}

// CallResult mirrors the tools/call response content array.
type CallResult struct {
	Content []Content
	IsError bool
}

// Content is one entry in CallResult.Content.
//
// v1 only renders text content. Image/audio/embedded-resource content types
// are flattened to Type+Text where Text is empty (or a marker). Future phases
// can expand this without breaking the v1 surface.
type Content struct {
	Type string
	Text string
}

// NewClient creates a client from the given server map. The map may be nil
// or empty (no-op client). Headers from each Server.Headers entry are sent
// on every request to that server; testagent does not inject any headers
// of its own.
func NewClient(servers map[string]Server) *Client {
	return &Client{
		config:  servers,
		servers: make(map[string]*serverConn),
	}
}

// SetDebugWriter attaches a writer that receives stderr from stdio-backed
// MCP servers. Each line is prefixed "mcp[<name>]: ". Passing nil disables
// passthrough. Must be called before Connect.
func (c *Client) SetDebugWriter(w io.Writer) {
	c.debugWriter = w
}

// Connect performs initialize + notifications/initialized + tools/list against
// every configured server in parallel. Populates the internal tool list.
//
// If config is nil or has no servers, Connect is a no-op.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("mcp: client closed")
	}
	if c.connected {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	if len(c.config) == 0 {
		c.mu.Lock()
		c.connected = true
		c.mu.Unlock()
		return nil
	}

	type result struct {
		name   string
		server *serverConn
		err    error
	}

	resultCh := make(chan result, len(c.config))
	var wg sync.WaitGroup
	for name, cfg := range c.config {
		wg.Add(1)
		go func(name string, cfg Server) {
			defer wg.Done()
			s, err := c.connectServer(ctx, name, cfg)
			resultCh <- result{name: name, server: s, err: err}
		}(name, cfg)
	}
	wg.Wait()
	close(resultCh)

	servers := make(map[string]*serverConn)
	var tools []Tool
	var errs []error
	for r := range resultCh {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		servers[r.name] = r.server
		for _, t := range r.server.tools {
			tools = append(tools, toTool(r.name, t))
		}
	}

	if len(errs) > 0 {
		// Tear down whatever did connect so Close is unambiguous.
		for _, s := range servers {
			_ = s.client.Close()
		}
		return errors.Join(errs...)
	}

	c.mu.Lock()
	c.servers = servers
	c.tools = tools
	c.connected = true
	c.mu.Unlock()
	return nil
}

// connectServer dispatches to connectHTTP or connectStdio based on the
// server's resolved transport type.
func (c *Client) connectServer(ctx context.Context, name string, cfg Server) (*serverConn, error) {
	switch resolveType(cfg) {
	case "http":
		return c.connectHTTP(ctx, name, cfg)
	case "stdio":
		return c.connectStdio(ctx, name, cfg)
	case "":
		return nil, fmt.Errorf("mcp %s: server config has neither URL nor Command", name)
	default:
		return nil, fmt.Errorf("mcp %s: unsupported transport type %q (want \"http\" or \"stdio\")", name, cfg.Type)
	}
}

// connectHTTP dials an HTTP MCP server and runs the handshake + tools/list.
func (c *Client) connectHTTP(ctx context.Context, name string, cfg Server) (*serverConn, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("mcp %s: empty URL", name)
	}

	headers := make(map[string]string, len(cfg.Headers))
	for k, v := range cfg.Headers {
		headers[k] = v
	}

	cl, err := client.NewStreamableHttpClient(
		cfg.URL,
		transport.WithHTTPHeaders(headers),
	)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", name, err)
	}

	if err := cl.Start(ctx); err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("mcp %s: start: %w", name, err)
	}

	initReq := mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcpgo.Implementation{
				Name:    "testagent",
				Version: "0.1.0",
			},
			Capabilities: mcpgo.ClientCapabilities{},
		},
	}
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("mcp %s: initialize: %w", name, err)
	}

	listRes, err := cl.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("mcp %s: tools/list: %w", name, err)
	}

	return &serverConn{
		name:   name,
		client: cl,
		tools:  listRes.Tools,
	}, nil
}

// connectStdio spawns a stdio MCP subprocess and runs the handshake + tools/list.
func (c *Client) connectStdio(ctx context.Context, name string, cfg Server) (*serverConn, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp %s: empty Command", name)
	}
	env := mergedEnv(cfg.Env)

	stdioTransport := transport.NewStdioWithOptions(cfg.Command, env, cfg.Args,
		transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
			cmd := exec.CommandContext(ctx, command, args...)
			cmd.Env = env
			shellrun.SetProcessGroup(cmd)
			return cmd, nil
		}),
	)

	cl := client.NewClient(stdioTransport)
	if err := cl.Start(ctx); err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("mcp %s: start: %w", name, err)
	}

	// Wire stderr passthrough if debugWriter is set.
	c.spawnStderrPump(name, cl)

	initReq := mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcpgo.Implementation{
				Name:    "testagent",
				Version: "0.1.0",
			},
			Capabilities: mcpgo.ClientCapabilities{},
		},
	}
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("mcp %s: initialize: %w", name, err)
	}

	listRes, err := cl.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("mcp %s: tools/list: %w", name, err)
	}

	return &serverConn{name: name, client: cl, tools: listRes.Tools}, nil
}

// spawnStderrPump launches a goroutine that copies the per-server stdio
// transport's stderr to c.debugWriter with a "mcp[<name>]: " line prefix.
// No-op when debugWriter is nil or the transport does not expose stderr.
// The goroutine ends when the reader hits EOF (subprocess exits or Close
// shuts the stderr pipe).
func (c *Client) spawnStderrPump(name string, cl *client.Client) {
	if c.debugWriter == nil {
		return
	}
	r, ok := client.GetStderr(cl)
	if !ok {
		return
	}
	go func() {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			fmt.Fprintf(c.debugWriter, "mcp[%s]: %s\n", name, sc.Text())
		}
	}()
}

// mergedEnv returns os.Environ() with extras appended (last-write wins on
// key collision). The returned slice is safe to mutate by the caller.
// Mirrors the env layering that internal/hooks + internal/codexhooks already
// do for command-type hooks.
func mergedEnv(extras map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	for k, v := range extras {
		env = append(env, k+"="+v)
	}
	return env
}

// Tools returns all tools across all connected servers, qualified as
// "<server>.<tool>". Returns nil if Connect has not been called or no
// servers are configured.
func (c *Client) Tools() []Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || len(c.tools) == 0 {
		return nil
	}
	out := make([]Tool, len(c.tools))
	copy(out, c.tools)
	return out
}

// Call invokes a tool by qualified name (e.g. "fileserver.read_file").
// Returns ErrNoServers if no MCP servers are configured.
// Returns ErrUnknownTool if the qualified name does not resolve.
func (c *Client) Call(ctx context.Context, qualifiedName string, args any) (CallResult, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return CallResult{}, errors.New("mcp: client closed")
	}
	if !c.connected || len(c.servers) == 0 {
		c.mu.Unlock()
		return CallResult{}, ErrNoServers
	}

	serverName, toolName, ok := strings.Cut(qualifiedName, ".")
	if !ok || serverName == "" || toolName == "" {
		c.mu.Unlock()
		return CallResult{}, fmt.Errorf("mcp: %w: %q (expected \"<server>.<tool>\")", ErrUnknownTool, qualifiedName)
	}
	srv, ok := c.servers[serverName]
	if !ok {
		c.mu.Unlock()
		return CallResult{}, fmt.Errorf("mcp: %w: server %q not connected", ErrUnknownTool, serverName)
	}
	// Verify the tool exists on this server (cheap pre-check; avoids a network
	// round-trip for typos and gives a clear error).
	var found bool
	for _, t := range srv.tools {
		if t.Name == toolName {
			found = true
			break
		}
	}
	c.mu.Unlock()

	if !found {
		return CallResult{}, fmt.Errorf("mcp %s: %w: tool %q", serverName, ErrUnknownTool, toolName)
	}

	req := mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}
	res, err := srv.client.CallTool(ctx, req)
	if err != nil {
		return CallResult{}, fmt.Errorf("mcp %s: tools/call %s: %w", serverName, toolName, err)
	}

	out := CallResult{IsError: res.IsError}
	for _, raw := range res.Content {
		out.Content = append(out.Content, fromContent(raw))
	}
	return out, nil
}

// Close cleanly disconnects from all servers. Safe to call multiple times.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	servers := c.servers
	c.servers = nil
	c.tools = nil
	c.mu.Unlock()

	var errs []error
	for name, s := range servers {
		if err := s.client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("mcp %s: close: %w", name, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// toTool converts a library Tool to the local Tool view. The library's
// Tool.MarshalJSON handles RawInputSchema vs structured InputSchema, so we
// marshal/unmarshal the InputSchema field directly.
func toTool(server string, t mcpgo.Tool) Tool {
	var schema json.RawMessage
	if len(t.RawInputSchema) > 0 {
		schema = append(json.RawMessage(nil), t.RawInputSchema...)
	} else {
		// Marshal the structured schema.
		if b, err := json.Marshal(t.InputSchema); err == nil {
			schema = b
		}
	}
	return Tool{
		Server:      server,
		Name:        t.Name,
		Description: t.Description,
		InputSchema: schema,
	}
}

// fromContent flattens a library Content interface into our v1 view.
// v1 only handles text; non-text types are reported with an empty Text body
// so callers can at least know what was returned.
func fromContent(raw mcpgo.Content) Content {
	switch v := raw.(type) {
	case mcpgo.TextContent:
		return Content{Type: v.Type, Text: v.Text}
	case *mcpgo.TextContent:
		return Content{Type: v.Type, Text: v.Text}
	case mcpgo.ImageContent:
		return Content{Type: v.Type}
	case *mcpgo.ImageContent:
		return Content{Type: v.Type}
	case mcpgo.AudioContent:
		return Content{Type: v.Type}
	case *mcpgo.AudioContent:
		return Content{Type: v.Type}
	}
	// Fallback: round-trip through JSON to read the discriminator.
	if b, err := json.Marshal(raw); err == nil {
		var probe struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(b, &probe)
		return Content{Type: probe.Type, Text: probe.Text}
	}
	return Content{}
}
