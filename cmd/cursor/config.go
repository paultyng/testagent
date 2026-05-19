package cursor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/paultyng/testagent/internal/cursorhooks"
	"github.com/paultyng/testagent/internal/mcp"
)

// Config is the combined project + user config loaded by loadConfig.
// Either field may be nil if the corresponding file is absent.
type Config struct {
	MCP   *MCPConfig
	Hooks *HooksConfig
}

// cursorMCPServer is the on-disk shape of one entry under .cursor/mcp.json's
// "mcpServers" map. Captures both HTTP transport fields (URL/Headers) and
// stdio transport fields (Command/Args/Env), plus the Cursor-specific Disabled
// toggle that mcp enable/disable round-trips. Convert to mcp.Server via
// toCoreServer() at the launch boundary.
type cursorMCPServer struct {
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// Disabled MUST NOT carry omitempty: omitempty hides false on
	// re-marshal, which would silently corrupt an enabled-after-disabled
	// round-trip if a future writer marshals via this struct rather than
	// the raw-map path used by toggleMCPServer.
	Disabled bool `json:"disabled"`
}

// toCoreServer projects a cursorMCPServer into the shared internal/mcp.Server
// shape. internal/mcp now supports stdio transport, so all fields project
// directly. Callers should still filter on Disabled.
func (s cursorMCPServer) toCoreServer() mcp.Server {
	return mcp.Server{
		Type:    s.Type,
		URL:     s.URL,
		Headers: s.Headers,
		Command: s.Command,
		Args:    s.Args,
		Env:     s.Env,
	}
}

// MCPConfig is the on-disk shape of .cursor/mcp.json and ~/.cursor/mcp.json.
// Uses cursorMCPServer to capture the full on-disk field set including stdio
// transport fields and the Disabled toggle. Conversion to mcp.Server happens
// at the launch boundary via toCoreServer().
type MCPConfig struct {
	MCPServers map[string]cursorMCPServer `json:"mcpServers"`
}

// HooksConfig is the on-disk shape of .cursor/hooks.json.
// Version must be 1; any other value is rejected by loadHooksConfig.
type HooksConfig struct {
	Version int                    `json:"version"`
	Hooks   map[string][]HookEntry `json:"hooks"`
}

// HookEntry is one hook handler under a Cursor hook event.
// Mirrors the schema at cursor.com/docs/hooks. Type defaults to "command"
// when absent; LoopLimit is a pointer so null and absent are distinguishable.
type HookEntry struct {
	Command    string `json:"command"`
	Type       string `json:"type,omitempty"`
	Matcher    string `json:"matcher,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
	LoopLimit  *int   `json:"loop_limit,omitempty"`
	FailClosed bool   `json:"failClosed,omitempty"`
}

// loadMCPConfig reads a single .cursor/mcp.json file at path. Returns
// (nil, nil) when the file does not exist; returns a wrapped error on
// I/O or JSON parse failure.
func loadMCPConfig(path string) (*MCPConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading cursor mcp.json %s: %w", path, err)
	}
	var c MCPConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing cursor mcp.json %s: %w", path, err)
	}
	return &c, nil
}

// loadHooksConfig reads a single .cursor/hooks.json file at path. Returns
// (nil, nil) when the file does not exist. Returns an error when version is
// missing (zero) or unsupported (not 1).
func loadHooksConfig(path string) (*HooksConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading cursor hooks.json %s: %w", path, err)
	}
	var c HooksConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing cursor hooks.json %s: %w", path, err)
	}
	if c.Version == 0 {
		return nil, fmt.Errorf("parsing cursor hooks.json %s: missing required \"version\" field", path)
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("parsing cursor hooks.json %s: unsupported version %d, want 1", path, c.Version)
	}
	return &c, nil
}

// loadConfig loads and merges the project-level and user-level Cursor configs
// for the given workspace directory. Project values WIN over user-level values
// for MCP server names that appear in both (project key overwrites global key).
// Returns (nil, nil) only if all config files are absent.
func loadConfig(workspace string) (*Config, error) {
	projectMCPPath := filepath.Join(workspace, ".cursor", "mcp.json")
	projectHooksPath := filepath.Join(workspace, ".cursor", "hooks.json")

	projectMCP, err := loadMCPConfig(projectMCPPath)
	if err != nil {
		return nil, err
	}

	hooks, err := loadHooksConfig(projectHooksPath)
	if err != nil {
		return nil, err
	}

	userMCPPath, err := userMCPConfigPath()
	if err != nil {
		return nil, err
	}
	userMCP, err := loadMCPConfig(userMCPPath)
	if err != nil {
		return nil, err
	}

	merged := mergeMCPConfigs(userMCP, projectMCP)

	if merged == nil && hooks == nil {
		return nil, nil
	}
	return &Config{
		MCP:   merged,
		Hooks: hooks,
	}, nil
}

// userMCPConfigPath returns the path to ~/.cursor/mcp.json.
//
// CURSOR_HOME, when set, replaces the user home directory in the resolution:
//
//	$CURSOR_HOME/.cursor/mcp.json
//
// Tests typically point CURSOR_HOME at t.TempDir() with a `.cursor/mcp.json`
// underneath. This is intentionally different from codex's CODEX_HOME, which
// names the config directory itself ($CODEX_HOME/config.toml). The cursor
// convention follows upstream cursor — the CLI also resolves $HOME/.cursor/.
func userMCPConfigPath() (string, error) {
	if home := os.Getenv("CURSOR_HOME"); home != "" {
		// filepath.Clean collapses ".." / "." / "//" so a malicious
		// CURSOR_HOME like "../../etc" can't compose with the trailing
		// ".cursor/mcp.json" to redirect the read/write into an
		// unexpected directory. Low impact (env is attacker-controlled
		// already), but cheap to harden.
		return filepath.Join(filepath.Clean(home), ".cursor", "mcp.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir for cursor config: %w", err)
	}
	return filepath.Join(home, ".cursor", "mcp.json"), nil
}

// mergeMCPConfigs merges base and overlay MCPConfigs. Keys in overlay WIN
// over keys in base. Either argument may be nil.
func mergeMCPConfigs(base, overlay *MCPConfig) *MCPConfig {
	if base == nil && overlay == nil {
		return nil
	}
	merged := &MCPConfig{
		MCPServers: make(map[string]cursorMCPServer),
	}
	if base != nil {
		for k, v := range base.MCPServers {
			merged.MCPServers[k] = v
		}
	}
	if overlay != nil {
		for k, v := range overlay.MCPServers {
			merged.MCPServers[k] = v
		}
	}
	return merged
}

// matchersFromConfig flattens a HooksConfig into the per-event matcher map
// the cursorhooks runner consumes. Each HookEntry becomes one Matcher with
// the entry's optional matcher pattern, type, timeout, loop limit, and
// fail-closed bit carried through. nil/empty cfg yields a nil result so the
// runner is a no-op.
func matchersFromConfig(cfg *HooksConfig) map[string][]cursorhooks.Matcher {
	if cfg == nil || len(cfg.Hooks) == 0 {
		return nil
	}
	out := make(map[string][]cursorhooks.Matcher, len(cfg.Hooks))
	for event, entries := range cfg.Hooks {
		conv := make([]cursorhooks.Matcher, 0, len(entries))
		for _, e := range entries {
			conv = append(conv, cursorhooks.Matcher{
				Pattern:    e.Matcher,
				Command:    e.Command,
				Type:       e.Type,
				Timeout:    e.Timeout,
				LoopLimit:  e.LoopLimit,
				FailClosed: e.FailClosed,
			})
		}
		if len(conv) > 0 {
			out[event] = conv
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// enabledServersFromConfig returns all enabled servers from the merged
// config in the shape internal/mcp.NewClient expects. Disabled servers
// are dropped; the transport (HTTP / stdio) is resolved by internal/mcp's
// connectServer dispatcher.
func enabledServersFromConfig(cfg *Config) map[string]mcp.Server {
	if cfg == nil || cfg.MCP == nil || len(cfg.MCP.MCPServers) == 0 {
		return nil
	}
	out := make(map[string]mcp.Server, len(cfg.MCP.MCPServers))
	for name, srv := range cfg.MCP.MCPServers {
		if srv.Disabled {
			continue
		}
		out[name] = srv.toCoreServer()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
