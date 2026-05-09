// MCP HTTP client. Connects to servers declared in --mcp-config, performs
// the standard MCP handshake (initialize + notifications/initialized +
// tools/list), and exposes a method to invoke tools.
//
// Built on github.com/mark3labs/mcp-go.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// ErrNoServers is returned by Call when no MCP servers are configured.
var ErrNoServers = errors.New("no MCP servers configured")

// ErrUnknownTool is returned by Call when the qualified tool name does not
// resolve to a known server+tool.
var ErrUnknownTool = errors.New("unknown tool")

// MCPClient connects to MCP servers declared in MCPConfig and exposes the
// union of their tools.
type MCPClient struct {
	config *MCPConfig

	mu        sync.Mutex
	servers   map[string]*mcpServer // keyed by server name
	tools     []MCPTool             // qualified, sorted by Server.Name
	connected bool
	closed    bool
}

// mcpServer holds the per-server client + its tool list.
type mcpServer struct {
	name   string
	client *client.Client
	tools  []mcp.Tool
}

// MCPTool is the union-of-tools view returned by Tools().
type MCPTool struct {
	Server      string
	Name        string
	Description string
	InputSchema json.RawMessage
}

// MCPCallResult mirrors the tools/call response content array.
type MCPCallResult struct {
	Content []MCPContent
	IsError bool
}

// MCPContent is one entry in MCPCallResult.Content.
//
// v1 only renders text content. Image/audio/embedded-resource content types
// are flattened to Type+Text where Text is empty (or a marker). Future phases
// can expand this without breaking the v1 surface.
type MCPContent struct {
	Type string
	Text string
}

// NewMCPClient creates a client from the given config. Config may be nil
// (no-op client). Headers from each MCPServer.Headers entry are sent on
// every request to that server; testagent does not inject any headers
// of its own.
func NewMCPClient(config *MCPConfig) *MCPClient {
	return &MCPClient{
		config:  config,
		servers: make(map[string]*mcpServer),
	}
}

// Connect performs initialize + notifications/initialized + tools/list against
// every configured server in parallel. Populates the internal tool list.
//
// If config is nil or has no servers, Connect is a no-op.
func (c *MCPClient) Connect(ctx context.Context) error {
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

	if c.config == nil || len(c.config.MCPServers) == 0 {
		c.mu.Lock()
		c.connected = true
		c.mu.Unlock()
		return nil
	}

	type result struct {
		name   string
		server *mcpServer
		err    error
	}

	resultCh := make(chan result, len(c.config.MCPServers))
	var wg sync.WaitGroup
	for name, cfg := range c.config.MCPServers {
		wg.Add(1)
		go func(name string, cfg MCPServer) {
			defer wg.Done()
			s, err := c.connectServer(ctx, name, cfg)
			resultCh <- result{name: name, server: s, err: err}
		}(name, cfg)
	}
	wg.Wait()
	close(resultCh)

	servers := make(map[string]*mcpServer)
	var tools []MCPTool
	var errs []error
	for r := range resultCh {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		servers[r.name] = r.server
		for _, t := range r.server.tools {
			tools = append(tools, toMCPTool(r.name, t))
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

// connectServer dials one server and runs the handshake + tools/list.
func (c *MCPClient) connectServer(ctx context.Context, name string, cfg MCPServer) (*mcpServer, error) {
	if cfg.Type != "" && cfg.Type != "http" {
		return nil, fmt.Errorf("mcp %s: unsupported transport type %q (only \"http\" supported)", name, cfg.Type)
	}
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

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "testagent",
				Version: "0.1.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	}
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("mcp %s: initialize: %w", name, err)
	}

	listRes, err := cl.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		_ = cl.Close()
		return nil, fmt.Errorf("mcp %s: tools/list: %w", name, err)
	}

	return &mcpServer{
		name:   name,
		client: cl,
		tools:  listRes.Tools,
	}, nil
}

// Tools returns all tools across all connected servers, qualified as
// "<server>.<tool>". Returns nil if Connect has not been called or no
// servers are configured.
func (c *MCPClient) Tools() []MCPTool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected || len(c.tools) == 0 {
		return nil
	}
	out := make([]MCPTool, len(c.tools))
	copy(out, c.tools)
	return out
}

// Call invokes a tool by qualified name (e.g. "fileserver.read_file").
// Returns ErrNoServers if no MCP servers are configured.
// Returns ErrUnknownTool if the qualified name does not resolve.
func (c *MCPClient) Call(ctx context.Context, qualifiedName string, args any) (MCPCallResult, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return MCPCallResult{}, errors.New("mcp: client closed")
	}
	if !c.connected || len(c.servers) == 0 {
		c.mu.Unlock()
		return MCPCallResult{}, ErrNoServers
	}

	serverName, toolName, ok := strings.Cut(qualifiedName, ".")
	if !ok || serverName == "" || toolName == "" {
		c.mu.Unlock()
		return MCPCallResult{}, fmt.Errorf("mcp: %w: %q (expected \"<server>.<tool>\")", ErrUnknownTool, qualifiedName)
	}
	srv, ok := c.servers[serverName]
	if !ok {
		c.mu.Unlock()
		return MCPCallResult{}, fmt.Errorf("mcp: %w: server %q not connected", ErrUnknownTool, serverName)
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
		return MCPCallResult{}, fmt.Errorf("mcp %s: %w: tool %q", serverName, ErrUnknownTool, toolName)
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}
	res, err := srv.client.CallTool(ctx, req)
	if err != nil {
		return MCPCallResult{}, fmt.Errorf("mcp %s: tools/call %s: %w", serverName, toolName, err)
	}

	out := MCPCallResult{IsError: res.IsError}
	for _, raw := range res.Content {
		out.Content = append(out.Content, fromMCPContent(raw))
	}
	return out, nil
}

// Close cleanly disconnects from all servers. Safe to call multiple times.
func (c *MCPClient) Close() error {
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

// toMCPTool converts a library Tool to the local MCPTool view. The library's
// Tool.MarshalJSON handles RawInputSchema vs structured InputSchema, so we
// marshal/unmarshal the InputSchema field directly.
func toMCPTool(server string, t mcp.Tool) MCPTool {
	var schema json.RawMessage
	if len(t.RawInputSchema) > 0 {
		schema = append(json.RawMessage(nil), t.RawInputSchema...)
	} else {
		// Marshal the structured schema.
		if b, err := json.Marshal(t.InputSchema); err == nil {
			schema = b
		}
	}
	return MCPTool{
		Server:      server,
		Name:        t.Name,
		Description: t.Description,
		InputSchema: schema,
	}
}

// fromMCPContent flattens a library Content interface into our v1 view.
// v1 only handles text; non-text types are reported with an empty Text body
// so callers can at least know what was returned.
func fromMCPContent(raw mcp.Content) MCPContent {
	switch v := raw.(type) {
	case mcp.TextContent:
		return MCPContent{Type: v.Type, Text: v.Text}
	case *mcp.TextContent:
		return MCPContent{Type: v.Type, Text: v.Text}
	case mcp.ImageContent:
		return MCPContent{Type: v.Type}
	case *mcp.ImageContent:
		return MCPContent{Type: v.Type}
	case mcp.AudioContent:
		return MCPContent{Type: v.Type}
	case *mcp.AudioContent:
		return MCPContent{Type: v.Type}
	}
	// Fallback: round-trip through JSON to read the discriminator.
	if b, err := json.Marshal(raw); err == nil {
		var probe struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(b, &probe)
		return MCPContent{Type: probe.Type, Text: probe.Text}
	}
	return MCPContent{}
}
