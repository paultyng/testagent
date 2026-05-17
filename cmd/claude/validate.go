package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/configvalidate"
	"github.com/paultyng/testagent/internal/hooks"
)

// Known Claude Code hook event names. The validate subcommand's job is
// "does this config match Claude Code's documented schema?" — orthogonal
// to whether testagent models the event at runtime. Events fired by
// testagent reuse the constants from internal/hooks; events Claude Code
// documents but testagent does not yet model are listed as bare strings.
var knownClaudeEvents = []string{
	// Events testagent fires + accepts.
	hooks.UserPromptSubmit,
	hooks.PreToolUse,
	hooks.PostToolUse,
	hooks.Stop,
	hooks.SessionStart,
	hooks.SessionEnd,
	hooks.PreCompact,
	hooks.PostCompact,
	hooks.Notification,
	hooks.PermissionRequest,
	// Documented by Claude Code, not yet modeled by testagent. Accepted
	// in --strict so configs subscribing to them validate without
	// forcing a runtime upgrade.
	"PostToolUseFailure",
	"StopFailure",
	"SubagentStart",
	"SubagentStop",
	"TaskCreated",
	"TaskCompleted",
	"Elicitation",
	"ElicitationResult",
}

// Known hook handler types for Claude. testagent dispatches "http" and
// "command"; other strings are typos in --strict mode.
var knownClaudeHookTypes = []string{"http", "command"}

// newValidateCommand returns `testagent claude validate`. Reuses the
// parent command's --settings / --mcp-config persistent flags by
// taking pointers to the parent's closure variables.
func newValidateCommand(settingsPath, mcpPath *string) *cobra.Command {
	var strict bool
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a Claude Code settings.json or mcp-config without booting a session",
		Long: `Reads the settings.json (--settings) and/or MCP config (--mcp-config) and
reports structural problems on stderr. Exits 0 when clean, 1 on validation
errors, 2 on usage error.

Lax mode (default) catches outright structural errors (malformed JSON,
wrong type for a known field). --strict additionally rejects unknown
fields, unknown event names, unknown hook types, and matchers with
zero hooks.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runValidate(cmd.ErrOrStderr(), *settingsPath, *mcpPath, strict)
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "reject unknown fields, unknown events, unknown hook types")
	return cmd
}

// runValidate is the testable body. Reports issues to stderr and
// returns an ExitError carrying the documented exit code.
func runValidate(stderr io.Writer, settingsPath, mcpPath string, strict bool) error {
	if settingsPath == "" && mcpPath == "" {
		fmt.Fprintln(stderr, "validate: supply at least one of --settings, --mcp-config")
		return &ExitError{Code: configvalidate.ExitUsageError}
	}
	var col configvalidate.Collector
	var usageErr error
	if settingsPath != "" {
		if err := validateSettings(settingsPath, strict, &col); err != nil {
			usageErr = err
		}
	}
	if mcpPath != "" {
		if err := validateMCPConfigFile(mcpPath, &col); err != nil && usageErr == nil {
			usageErr = err
		}
	}
	col.Print(stderr)
	if usageErr != nil {
		fmt.Fprintf(stderr, "validate: %v\n", usageErr)
	}
	code := configvalidate.ExitCode(&col, usageErr)
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

// validateSettings reads + decodes the settings.json file. JSON parse
// errors land as Issues with line/col resolved from the decoder's Offset.
// Returns a non-nil error only on file-system errors (missing file etc.)
// that map to ExitUsageError.
func validateSettings(path string, strict bool, col *configvalidate.Collector) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if strict {
		dec.DisallowUnknownFields()
	}
	var s Settings
	if err := dec.Decode(&s); err != nil {
		addJSONDecodeIssue(col, path, data, err)
		return nil
	}
	validateSettingsRules(path, &s, strict, col)
	return nil
}

// validateSettingsRules applies the per-rule checks against a decoded
// Settings value. Lax mode performs no extra checks; --strict gates the
// hook-event + hook-type allowlists and structural minimums.
func validateSettingsRules(path string, s *Settings, strict bool, col *configvalidate.Collector) {
	if !strict || s == nil {
		return
	}
	for event, matchers := range s.Hooks {
		if !configvalidate.ContainsStr(knownClaudeEvents, event) {
			col.Addf(path, 0, "unknown hook event %q (%s)", event, configvalidate.Suggest(event, knownClaudeEvents))
		}
		if len(matchers) == 0 {
			col.Addf(path, 0, "hooks.%s has zero matchers", event)
			continue
		}
		for i, m := range matchers {
			if len(m.Hooks) == 0 {
				col.Addf(path, 0, "hooks.%s[%d] has zero hooks", event, i)
				continue
			}
			for j, h := range m.Hooks {
				if !configvalidate.ContainsStr(knownClaudeHookTypes, h.Type) {
					col.Addf(path, 0, "hooks.%s[%d].hooks[%d] unknown type %q (%s)",
						event, i, j, h.Type, configvalidate.Suggest(h.Type, knownClaudeHookTypes))
					continue
				}
				switch h.Type {
				case "http":
					if h.URL == "" {
						col.Addf(path, 0, "hooks.%s[%d].hooks[%d] type=http requires url", event, i, j)
					}
				case "command":
					if h.Command == "" {
						col.Addf(path, 0, "hooks.%s[%d].hooks[%d] type=command requires command", event, i, j)
					}
				}
			}
		}
	}
}

// validateMCPConfigFile is parse-only today; per-server schema rules
// are tracked in #93. Drop the strict flag from the signature so a
// future reader doesn't think the lax/strict split is already wired.
func validateMCPConfigFile(path string, col *configvalidate.Collector) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	var m MCPConfig
	if err := json.Unmarshal(data, &m); err != nil {
		addJSONDecodeIssue(col, path, data, err)
	}
	return nil
}

// addJSONDecodeIssue maps a decoder error to an Issue with line/col
// resolved when the underlying error carries an Offset. Falls back to
// the path-only form when no offset is available.
func addJSONDecodeIssue(col *configvalidate.Collector, path string, data []byte, err error) {
	var (
		syn *json.SyntaxError
		typ *json.UnmarshalTypeError
	)
	switch {
	case errors.As(err, &syn):
		line, col2 := configvalidate.LineColAt(data, syn.Offset)
		col.Add(configvalidate.Issue{Path: path, Line: line, Col: col2, Msg: "syntax: " + syn.Error()})
	case errors.As(err, &typ):
		line, col2 := configvalidate.LineColAt(data, typ.Offset)
		msg := fmt.Sprintf("invalid value for field %q: %s", typ.Field, typ.Error())
		col.Add(configvalidate.Issue{Path: path, Line: line, Col: col2, Msg: msg})
	case errors.Is(err, io.ErrUnexpectedEOF):
		// Truncated input — no Offset available; report at the end of file.
		line, col2 := configvalidate.LineColAt(data, int64(len(data)))
		col.Add(configvalidate.Issue{Path: path, Line: line, Col: col2, Msg: "unexpected end of JSON input"})
	default:
		col.Addf(path, 0, "%v", err)
	}
}

