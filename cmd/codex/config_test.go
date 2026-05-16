package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/paultyng/testagent/internal/codexhooks"
)

// TestLoadConfig_UpstreamHookSchema asserts that a config.toml file
// using codex's canonical hook shape (event → MatcherGroup{matcher,
// hooks[]}, with hook.type discriminating command/prompt/agent)
// decodes and flattens correctly. Regression for #54 where the
// schema was previously modeled flat and realistic codex configs
// failed to decode their command hooks.
func TestLoadConfig_UpstreamHookSchema(t *testing.T) {
	t.Parallel()

	input := `
[[hooks.SessionStart]]
matcher = "*"
[[hooks.SessionStart.hooks]]
type = "command"
command = "echo session-started"
timeout = 5

[[hooks.UserPromptSubmit]]
[[hooks.UserPromptSubmit.hooks]]
type = "command"
command = "echo got-prompt"
async = true
[[hooks.UserPromptSubmit.hooks]]
type = "prompt"
prompt = "ignored-by-testagent"
`
	var c Config
	if _, err := toml.Decode(input, &c); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := matchersFromConfig(&c)
	want := map[string][]codexhooks.Matcher{
		"SessionStart": {
			{Pattern: "*", Command: "echo session-started", Timeout: 5},
		},
		"UserPromptSubmit": {
			{Pattern: "", Command: "echo got-prompt", Async: true},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("matchersFromConfig mismatch:\n  got=  %#v\n  want= %#v", got, want)
	}
}

// TestLoadConfig_EmptyAndUnknownEvents covers the no-op edge cases:
// a missing [hooks] table, an event whose groups have only non-
// command hooks (filtered out → no entry), and an event with no
// hooks at all. None should appear in the flattened map.
func TestLoadConfig_EmptyAndUnknownEvents(t *testing.T) {
	t.Parallel()

	input := `
[[hooks.PromptOnly]]
[[hooks.PromptOnly.hooks]]
type = "prompt"
prompt = "x"

[[hooks.EmptyGroup]]
matcher = "*"
`
	var c Config
	if _, err := toml.Decode(input, &c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := matchersFromConfig(&c); got != nil {
		t.Errorf("expected nil flatten result, got %#v", got)
	}
}

// TestLoadConfig_FileRoundtrip exercises loadConfig's file IO with a
// realistic input, ensuring the path resolver and decoder agree on
// the schema shape end-to-end.
func TestLoadConfig_FileRoundtrip(t *testing.T) {
	// Not parallel: uses t.Setenv to point CODEX_HOME at our tempdir.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[[hooks.Stop]]
[[hooks.Stop.hooks]]
type = "command"
command = "true"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", dir)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("loadConfig returned nil cfg, want non-nil")
	}
	groups, ok := cfg.Hooks["Stop"]
	if !ok || len(groups) != 1 || len(groups[0].Hooks) != 1 {
		t.Fatalf("unexpected Stop hooks: %#v", cfg.Hooks)
	}
	if got := groups[0].Hooks[0]; got.Type != "command" || got.Command != "true" {
		t.Errorf("Stop hook decoded wrong: %#v", got)
	}
}
