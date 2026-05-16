package codex

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
	"github.com/paultyng/testagent/internal/configvalidate"
	"github.com/paultyng/testagent/internal/codexhooks"
)

// Known codex hook event names. TOML keys are snake_case. Mirrors the
// constants in internal/codexhooks; inline here so the strict allowlist
// evolves with this file.
var knownCodexEvents = []string{
	codexhooks.EventSessionStart,
	codexhooks.EventUserPromptSubmit,
	codexhooks.EventPreToolUse,
	codexhooks.EventPostToolUse,
	codexhooks.EventStop,
	codexhooks.EventPreCompact,
	codexhooks.EventPostCompact,
	codexhooks.EventPermissionRequest,
}

// Known hook handler types in codex's TOML schema. testagent fires only
// "command" today; "prompt" and "agent" decode cleanly for forward
// compat and are accepted by --strict so existing configs validate.
var knownCodexHookTypes = []string{"command", "prompt", "agent"}

// newValidateCommand returns `testagent codex validate`. Reads
// $CODEX_HOME/config.toml (or ~/.codex/config.toml) via the same
// resolver the interactive subcommand uses.
func newValidateCommand() *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the codex config.toml (at $CODEX_HOME/config.toml) without booting a session",
		Long: `Reads the codex config from $CODEX_HOME/config.toml (or
~/.codex/config.toml when CODEX_HOME is unset) and reports structural
problems on stderr. Exits 0 when clean, 1 on validation errors, 2 on
usage error (e.g. file not found).

Lax mode (default) catches malformed TOML. --strict additionally
rejects unknown keys, unknown event names, unknown hook types, and
matchers with zero hooks.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runValidate(cmd.ErrOrStderr(), strict)
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "reject unknown keys, unknown events, unknown hook types")
	return cmd
}

// runValidate is the testable body.
func runValidate(stderr io.Writer, strict bool) error {
	path, err := configPath()
	if err != nil {
		fmt.Fprintf(stderr, "validate: %v\n", err)
		return &claude.ExitError{Code: configvalidate.ExitUsageError}
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "validate: %s does not exist\n", path)
			return &claude.ExitError{Code: configvalidate.ExitUsageError}
		}
		fmt.Fprintf(stderr, "validate: %v\n", err)
		return &claude.ExitError{Code: configvalidate.ExitUsageError}
	}
	var col configvalidate.Collector
	validateConfigFile(path, strict, &col)
	col.Print(stderr)
	code := configvalidate.ExitCode(&col, nil)
	if code != 0 {
		return &claude.ExitError{Code: code}
	}
	return nil
}

// validateConfigFile decodes the TOML and applies per-rule checks.
// BurntSushi's decoder doesn't surface line numbers for unknown-key
// errors via the returned MetaData, so issues are reported with the
// path only — backlog item to plumb line info later.
func validateConfigFile(path string, strict bool, col *configvalidate.Collector) {
	var c Config
	meta, err := toml.DecodeFile(path, &c)
	if err != nil {
		col.Addf(path, 0, "%v", err)
		return
	}
	if !strict {
		return
	}
	for _, k := range meta.Undecoded() {
		col.Addf(path, 0, "unknown key %q", k.String())
	}
	for event, groups := range c.Hooks {
		if !configvalidate.ContainsStr(knownCodexEvents, event) {
			col.Addf(path, 0, "unknown hook event %q (%s)", event, configvalidate.Suggest(event, knownCodexEvents))
		}
		if len(groups) == 0 {
			col.Addf(path, 0, "hooks.%s has zero matcher groups", event)
			continue
		}
		for i, g := range groups {
			if len(g.Hooks) == 0 {
				col.Addf(path, 0, "hooks.%s[%d] has zero hooks", event, i)
				continue
			}
			for j, h := range g.Hooks {
				if !configvalidate.ContainsStr(knownCodexHookTypes, h.Type) {
					col.Addf(path, 0, "hooks.%s[%d].hooks[%d] unknown type %q (%s)",
						event, i, j, h.Type, configvalidate.Suggest(h.Type, knownCodexHookTypes))
					continue
				}
				switch h.Type {
				case "command":
					if h.Command == "" {
						col.Addf(path, 0, "hooks.%s[%d].hooks[%d] type=command requires command", event, i, j)
					}
				case "prompt":
					if h.Prompt == "" {
						col.Addf(path, 0, "hooks.%s[%d].hooks[%d] type=prompt requires prompt", event, i, j)
					}
				case "agent":
					if h.Agent == "" {
						col.Addf(path, 0, "hooks.%s[%d].hooks[%d] type=agent requires agent", event, i, j)
					}
				}
			}
		}
	}
}

