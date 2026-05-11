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

// HooksTable maps a codex hook event name (e.g. "SessionStart") to a
// list of MatcherGroups. Each group filters by `matcher` (currently
// unused — accepted for forward compatibility) and contains a list of
// concrete hook entries. Wired through codexhooks.Runner in
// cmd/codex.runInteractive via matchersFromConfig.
//
// Mirrors upstream codex's schema:
//
//	[[hooks.SessionStart]]
//	matcher = "..."
//	[[hooks.SessionStart.hooks]]
//	type = "command"
//	command = "..."
type HooksTable map[string][]MatcherGroup

// MatcherGroup is one entry under a `[hooks.<event>]` array. The
// matcher pattern (when codex eventually wires it) selects which
// events the contained hooks respond to; for the MVP we run every
// hook unconditionally.
type MatcherGroup struct {
	Matcher string      `toml:"matcher"`
	Hooks   []HookEntry `toml:"hooks"`
}

// HookEntry is one concrete hook handler under a MatcherGroup.
// Type discriminates between codex's three handler shapes:
//
//   - "command": run a shell command (the only type testagent fires today)
//   - "prompt":  inject a prompt (accepted-but-ignored; tracked in a follow-up)
//   - "agent":   delegate to a sub-agent (accepted-but-ignored)
//
// All type-specific fields live on this struct as omitempty; the
// runner reads only the ones relevant to Type.
type HookEntry struct {
	Type    string `toml:"type"`
	Command string `toml:"command,omitempty"`
	Prompt  string `toml:"prompt,omitempty"`
	Agent   string `toml:"agent,omitempty"`
	Timeout int    `toml:"timeout,omitempty"`
	Async   bool   `toml:"async,omitempty"`
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
