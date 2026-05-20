package cursor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/cmd/claude"
	"github.com/paultyng/testagent/internal/configvalidate"
	"github.com/paultyng/testagent/internal/cursorhooks"
)

// knownCursorEvents is the allowlist of documented cursor hook event names.
// Used in strict mode to flag unknown event keys with did-you-mean suggestions.
var knownCursorEvents = []string{
	cursorhooks.EventBeforeShellExecution,
	cursorhooks.EventBeforeReadFile,
	cursorhooks.EventBeforeMCPExecution,
	cursorhooks.EventPreToolUse,
	cursorhooks.EventSubagentStart,
	cursorhooks.EventAfterShellExecution,
	cursorhooks.EventAfterFileEdit,
	cursorhooks.EventAfterMCPExecution,
	cursorhooks.EventSubagentStop,
	cursorhooks.EventAgentResponse,
	cursorhooks.EventStop,
}

// knownCursorHookTypes is the allowlist of documented cursor hook handler types.
// Empty type defaults to "command" and is accepted without error.
var knownCursorHookTypes = []string{"command", "prompt"}

func newValidateCommand() *cobra.Command {
	var (
		strict    bool
		workspace string
	)
	cmd := &cobra.Command{
		Use:          "validate",
		Short:        "Validate .cursor/{mcp,hooks}.json without booting a session",
		Long:         `Validate the cursor config files (mcp.json and hooks.json) without booting a session. Exit 0 means clean; exit 1 means validation errors on stderr; exit 2 means usage error. --strict adds unknown-event and unknown-hook-type checks.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runValidate(cmd.ErrOrStderr(), workspace, strict)
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "reject unknown events and unknown hook types")
	cmd.Flags().StringVar(&workspace, "workspace", "", "workspace root (default: current directory)")
	return cmd
}

func runValidate(stderr io.Writer, workspace string, strict bool) error {
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "validate: %v\n", err)
			return &claude.ExitError{Code: configvalidate.ExitUsageError}
		}
		workspace = cwd
	}

	var col configvalidate.Collector
	cursorDir := filepath.Join(workspace, ".cursor")

	mcpPath := filepath.Join(cursorDir, "mcp.json")
	if _, err := os.Stat(mcpPath); err == nil {
		validateMCPFile(mcpPath, &col)
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "validate: %v\n", err)
		return &claude.ExitError{Code: configvalidate.ExitUsageError}
	}

	hooksPath := filepath.Join(cursorDir, "hooks.json")
	if _, err := os.Stat(hooksPath); err == nil {
		validateHooksFile(hooksPath, strict, &col)
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "validate: %v\n", err)
		return &claude.ExitError{Code: configvalidate.ExitUsageError}
	}

	col.Print(stderr)
	code := configvalidate.ExitCode(&col, nil)
	if code != 0 {
		return &claude.ExitError{Code: code}
	}
	return nil
}

// validateMCPFile parses mcp.json and records any parse errors into col.
func validateMCPFile(path string, col *configvalidate.Collector) {
	if _, err := loadMCPConfig(path); err != nil {
		col.Addf(path, 0, "%v", err)
	}
}

// validateHooksFile parses hooks.json and applies per-rule checks into col.
func validateHooksFile(path string, strict bool, col *configvalidate.Collector) {
	cfg, err := loadHooksConfig(path)
	if err != nil {
		col.Addf(path, 0, "%v", err)
		return
	}
	if !strict {
		return
	}
	for event, entries := range cfg.Hooks {
		if !configvalidate.ContainsStr(knownCursorEvents, event) {
			col.Addf(path, 0, "unknown hook event %q (%s)", event,
				configvalidate.Suggest(event, knownCursorEvents))
		}
		for _, entry := range entries {
			if entry.Type != "" && !configvalidate.ContainsStr(knownCursorHookTypes, entry.Type) {
				col.Addf(path, 0, "unknown hook type %q (%s)", entry.Type,
					configvalidate.Suggest(entry.Type, knownCursorHookTypes))
			}
		}
	}
}
