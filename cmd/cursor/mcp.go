package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/paultyng/testagent/internal/mcp"
	"github.com/paultyng/testagent/internal/rootflags"
)

// newMCPCommand wires the "mcp" parent subcommand and its four children.
// Real wiring lives here (not stubs); see Phase 2 for stdio-transport support.
// cf carries the parent cursor command's --workspace value; list and
// list-tools resolve their workspace through it (falling back to cwd).
func newMCPCommand(rf *rootflags.Flags, cf *flags) *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "Manage Cursor MCP servers", SilenceUsage: true}
	cmd.AddCommand(newMCPListCommand(rf, cf))
	cmd.AddCommand(newMCPListToolsCommand(rf, cf))
	cmd.AddCommand(newMCPEnableCommand(rf))
	cmd.AddCommand(newMCPDisableCommand(rf))
	return cmd
}

// resolveWorkspace returns the workspace path: cf.Workspace when set, else
// os.Getwd(). Entrypoints (RunE handlers) call this to convert the persistent
// --workspace flag into an explicit path argument, so downstream loaders don't
// touch process-global state.
func resolveWorkspace(cf *flags) (string, error) {
	if cf != nil && cf.Workspace != "" {
		return cf.Workspace, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}
	return cwd, nil
}

// newMCPListCommand prints each configured MCP server as one tab-separated
// line: name, enabled/disabled status, and transport type. Reads merged config
// from cf.Workspace (or cwd if unset).
func newMCPListCommand(_ *rootflags.Flags, cf *flags) *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List configured MCP servers",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, err := resolveWorkspace(cf)
			if err != nil {
				return err
			}
			cfg, err := loadConfig(ws)
			if err != nil {
				return err
			}
			if cfg == nil || cfg.MCP == nil || len(cfg.MCP.MCPServers) == 0 {
				return nil
			}

			names := make([]string, 0, len(cfg.MCP.MCPServers))
			for name := range cfg.MCP.MCPServers {
				names = append(names, name)
			}
			sort.Strings(names)

			for _, name := range names {
				srv := cfg.MCP.MCPServers[name]
				status := "enabled"
				if srv.Disabled {
					status = "disabled"
				}
				transport := mcpTransport(srv)
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", name, status, transport)
			}
			return nil
		},
	}
}

// mcpTransport returns a human-readable transport label for a cursorMCPServer.
// Prefers the explicit Type field; falls back to inferring stdio from Command.
func mcpTransport(srv cursorMCPServer) string {
	switch srv.Type {
	case "http":
		return "http"
	case "stdio":
		return "stdio"
	case "":
		if srv.Command != "" {
			return "stdio"
		}
		if srv.URL != "" {
			return "http"
		}
		return "(unset)"
	default:
		return srv.Type
	}
}

// newMCPListToolsCommand connects to a named MCP server (HTTP or stdio) and
// prints its tools. Errors immediately for disabled or missing servers. Reads
// merged config from cf.Workspace (or cwd if unset).
func newMCPListToolsCommand(_ *rootflags.Flags, cf *flags) *cobra.Command {
	return &cobra.Command{
		Use:          "list-tools <server>",
		Short:        "List tools exposed by an MCP server",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			serverName := args[0]

			ws, err := resolveWorkspace(cf)
			if err != nil {
				return err
			}
			cfg, err := loadConfig(ws)
			if err != nil {
				return err
			}

			var servers map[string]cursorMCPServer
			if cfg != nil && cfg.MCP != nil {
				servers = cfg.MCP.MCPServers
			}

			srv, ok := servers[serverName]
			if !ok {
				return fmt.Errorf("server %q not found in config", serverName)
			}
			if srv.Disabled {
				return fmt.Errorf("server %q is disabled", serverName)
			}

			// 60s budget covers cold-cache npx-installed servers (the
			// first-run download for @modelcontextprotocol/server-* is
			// often 10-30s on fresh CI runners). HTTP transports
			// complete in well under a second; the same ceiling is
			// fine for both transport types.
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			core := srv.toCoreServer()
			client := mcp.NewClient(map[string]mcp.Server{serverName: core})
			if err := client.Connect(ctx); err != nil {
				return fmt.Errorf("connecting to server %q: %w", serverName, err)
			}
			defer func() {
				if cerr := client.Close(); cerr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "cursor: mcp client close: %v\n", cerr)
				}
			}()

			tools := client.Tools()
			sort.Slice(tools, func(i, j int) bool {
				return tools[i].Name < tools[j].Name
			})
			for _, t := range tools {
				qualifiedName := t.Server + "." + t.Name
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", qualifiedName, t.Description)
			}
			return nil
		},
	}
}

// newMCPEnableCommand clears the disabled flag for a named server in
// ~/.cursor/mcp.json, writing back atomically to preserve unknown keys.
func newMCPEnableCommand(_ *rootflags.Flags) *cobra.Command {
	return &cobra.Command{
		Use:          "enable <server>",
		Short:        "Enable an MCP server",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return toggleMCPServer(args[0], false)
		},
	}
}

// newMCPDisableCommand sets the disabled flag for a named server in
// ~/.cursor/mcp.json, writing back atomically to preserve unknown keys.
func newMCPDisableCommand(_ *rootflags.Flags) *cobra.Command {
	return &cobra.Command{
		Use:          "disable <server>",
		Short:        "Disable an MCP server",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return toggleMCPServer(args[0], true)
		},
	}
}

// toggleMCPServer sets or clears the "disabled" field for serverName in the
// user's ~/.cursor/mcp.json. Uses a raw map[string]any round-trip so unknown
// top-level keys are preserved. Writes atomically via a .tmp rename.
func toggleMCPServer(serverName string, disabled bool) error {
	path, err := userMCPConfigPath()
	if err != nil {
		return err
	}
	raw, err := readUserMCPRaw(path)
	if err != nil {
		return err
	}
	if err := setServerDisabled(raw, path, serverName, disabled); err != nil {
		return err
	}
	return writeUserMCPAtomic(path, raw)
}

// readUserMCPRaw reads path as a JSON object into a generic map so unknown
// top-level keys survive the round-trip. Returns a specific not-found error
// when the file is absent so callers can offer the user a remediation hint.
func readUserMCPRaw(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s does not exist; create it with at least {\"mcpServers\":{}} before toggling servers", path)
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return raw, nil
}

// setServerDisabled mutates raw in place: sets entry["disabled"] = true when
// disabled is true, or removes the key when false. path is only used in error
// messages. Errors when mcpServers is absent / wrong type, or the named server
// is missing / wrong type.
func setServerDisabled(raw map[string]any, path, serverName string, disabled bool) error {
	serversAny, ok := raw["mcpServers"]
	if !ok {
		return fmt.Errorf("server %q not found in user mcp.json", serverName)
	}
	servers, ok := serversAny.(map[string]any)
	if !ok {
		return fmt.Errorf("mcpServers in %s is not an object", path)
	}
	entryAny, ok := servers[serverName]
	if !ok {
		return fmt.Errorf("server %q not found in user mcp.json", serverName)
	}
	entry, ok := entryAny.(map[string]any)
	if !ok {
		return fmt.Errorf("server %q in %s is not an object", serverName, path)
	}
	if disabled {
		entry["disabled"] = true
	} else {
		delete(entry, "disabled")
	}
	servers[serverName] = entry
	return nil
}

// writeUserMCPAtomic marshals raw and writes it to path via a temp-file +
// rename. Uses os.CreateTemp with a random suffix (rather than a predictable
// path+".tmp") so a hostile pre-existing symlink cannot redirect the write
// to an arbitrary file. The temp file is created in the same directory as
// path so the final Rename is on-device. On any failure after creation, the
// temp file is removed so we never leave behind a stale half-write.
func writeUserMCPAtomic(path string, raw map[string]any) error {
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	out = append(out, '\n')

	f, err := os.CreateTemp(filepath.Dir(path), "mcp-*.json")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", filepath.Dir(path), err)
	}
	tmp := f.Name()
	cleanup := func() { _ = os.Remove(tmp) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if _, err := f.Write(out); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		cleanup()
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
