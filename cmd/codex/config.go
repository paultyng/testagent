package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config mirrors the subset of `~/.codex/config.toml` the MVP consumes.
// Unknown TOML keys are tolerated — the loader uses BurntSushi/toml's
// default decoder, which silently ignores fields that don't appear here.
type Config struct {
	MCPServers map[string]MCPServer `toml:"mcp_servers"`
	Hooks      HooksTable           `toml:"hooks"`
}

// MCPServer is one entry under [mcp_servers.<name>] in the codex TOML.
// Codex servers can use stdio or HTTP transports; we capture both shapes
// even though MVP only consumes [mcp_servers] presence.
type MCPServer struct {
	Type    string            `toml:"type"`
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`
}

// HooksTable maps a codex hook event name (e.g. "session_start") to a
// list of matcher groups. Each group has a shell `command` invoked when
// the event fires. Wired through codexhooks.Runner in
// cmd/codex.runInteractive via matchersFromConfig.
type HooksTable map[string][]HookMatcher

// HookMatcher is one entry in a codex hook event's matcher array.
type HookMatcher struct {
	Command       string `toml:"command"`
	Async         bool   `toml:"async"`
	Timeout       int    `toml:"timeout"`
	StatusMessage string `toml:"statusMessage"`
}

// loadConfig reads `$CODEX_HOME/config.toml` (or `~/.codex/config.toml`
// when CODEX_HOME is unset). Returns nil + nil error when the file
// doesn't exist — codex tolerates a missing config and so does testagent.
// A malformed file returns an error so the user sees the parse failure
// at startup instead of silently dropping behavior.
func loadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat codex config %s: %w", path, err)
	}
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("parsing codex config %s: %w", path, err)
	}
	return &c, nil
}

// configPath resolves the codex config file path, honoring CODEX_HOME.
func configPath() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir for codex config: %w", err)
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}
