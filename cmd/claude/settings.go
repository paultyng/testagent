package claude

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/paultyng/testagent/internal/hooks"
	"github.com/paultyng/testagent/internal/mcp"
)

// Settings mirrors Claude Code's settings.json shape (hooks + permissions).
type Settings struct {
	Hooks       map[string][]hooks.Matcher `json:"hooks,omitempty"`
	Permissions *Permissions               `json:"permissions,omitempty"`
}

type Permissions struct {
	Allow []string `json:"allow,omitempty"`
}

// MCPConfig mirrors Claude Code's --mcp-config file shape.
type MCPConfig struct {
	MCPServers map[string]mcp.Server `json:"mcpServers"`
}

// loadSettings parses a Claude Code settings.json file. Empty path returns nil.
func loadSettings(path string) (*Settings, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading settings %s: %w", path, err)
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parsing settings %s: %w", path, err)
	}
	return &s, nil
}

// loadMCPConfig parses a Claude Code --mcp-config file. Empty path returns nil.
func loadMCPConfig(path string) (*MCPConfig, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading mcp config %s: %w", path, err)
	}
	var c MCPConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parsing mcp config %s: %w", path, err)
	}
	return &c, nil
}

// loadedStatus returns a one-line summary of what was loaded from flags / config
// files, suitable for display under the banner. Empty when nothing was loaded.
func loadedStatus(s *Settings, m *MCPConfig, systemPrompt string, addDirs []string) string {
	var parts []string
	if s != nil && len(s.Hooks) > 0 {
		names := make([]string, 0, len(s.Hooks))
		for k := range s.Hooks {
			names = append(names, strings.ToLower(k))
		}
		sort.Strings(names)
		parts = append(parts, "hooks: "+strings.Join(names, ", "))
	}
	if m != nil && len(m.MCPServers) > 0 {
		names := make([]string, 0, len(m.MCPServers))
		for k := range m.MCPServers {
			names = append(names, k)
		}
		sort.Strings(names)
		parts = append(parts, "mcp: "+strings.Join(names, ", "))
	}
	if systemPrompt != "" {
		parts = append(parts, fmt.Sprintf("system prompt: %d chars", len(systemPrompt)))
	}
	if len(addDirs) > 0 {
		parts = append(parts, fmt.Sprintf("dirs: %d", len(addDirs)))
	}
	return strings.Join(parts, " | ")
}

// newSessionID generates a UUID-v4-shaped session identifier.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}
