package cursor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/paultyng/testagent/internal/mcp"
)

// Config is the combined project + user config loaded by loadConfig.
// Either field may be nil if the corresponding file is absent.
type Config struct {
	MCP   *MCPConfig
	Hooks *HooksConfig
}

// MCPConfig is the on-disk shape of .cursor/mcp.json and ~/.cursor/mcp.json.
// The schema is identical to Claude Code's --mcp-config format. Note that
// mcp.Server only carries HTTP transport fields (Type, URL, Headers); stdio
// servers parsed from disk will have Type="stdio" but Command/Args/Env are
// silently dropped — Phase 2 should extend mcp.Server if stdio support is
// needed.
type MCPConfig struct {
	MCPServers map[string]mcp.Server `json:"mcpServers"`
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

// userMCPConfigPath returns the path to ~/.cursor/mcp.json. Honors the
// CURSOR_HOME environment variable when set (mirrors codex's CODEX_HOME
// pattern).
func userMCPConfigPath() (string, error) {
	if home := os.Getenv("CURSOR_HOME"); home != "" {
		return filepath.Join(home, ".cursor", "mcp.json"), nil
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
		MCPServers: make(map[string]mcp.Server),
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
