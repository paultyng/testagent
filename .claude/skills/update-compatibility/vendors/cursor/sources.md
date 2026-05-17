# Sources — cursor vendor

## Local binary recipes

```sh
# Version (also sets the "Upstream version researched" header).
cursor agent --version

# Root help (top-level commands)
cursor agent --help

# Subcommand helps — these are safe (no UI launch)
cursor agent mcp --help
cursor agent mcp login --help
cursor agent mcp list --help
cursor agent mcp list-tools --help
cursor agent mcp enable --help
cursor agent mcp disable --help
cursor agent status --help
cursor agent about --help
cursor agent models --help
cursor agent login --help
cursor agent logout --help
cursor agent update --help
cursor agent create-chat --help
cursor agent generate-rule --help
cursor agent ls --help
cursor agent resume --help
cursor agent install-shell-integration --help

# DO NOT invoke `cursor agent` interactively — bare command launches the
# UI / makes an API call. `--print --output-format ...` is also unsafe
# without auth; document the shape from upstream docs instead.
```

## ~/.cursor/ config inspection

These files exist on a machine with Cursor installed. Read-only inspection
to confirm schema during research:

```sh
# CLI config (approvalMode, sandbox, permissions, etc.)
jq 'keys' ~/.cursor/cli-config.json

# MCP servers
jq 'keys' ~/.cursor/mcp.json
jq '.mcpServers' ~/.cursor/mcp.json

# Agent CLI state (transient)
jq 'keys' ~/.cursor/agent-cli-state.json
```

## Upstream doc URLs

| Page | URL | What it provides |
|------|-----|------------------|
| CLI reference (top-level) | `https://cursor.com/docs/cli/using` | Interactive REPL, shortcuts, slash commands |
| CLI output formats | `https://cursor.com/docs/cli/reference/output-format` | `--print` NDJSON stream shape per tool variant |
| CLI permissions | `https://cursor.com/docs/cli/reference/permissions` | `approvalMode`, allowlist tokens (`Shell(...)`, `Read(...)`, `Mcp(server:tool)`) |
| Hooks | `https://cursor.com/docs/hooks` | All hook events + wire shape (top-level `permission`) + 4-level priority cascade |
| Rules | `https://cursor.com/docs/rules` | `.cursor/rules/*.mdc` frontmatter + activation modes |
| Plugins | `https://cursor.com/docs/plugins` | Plugin directory layout + component discovery |
| Sandbox reference | `https://cursor.com/docs/reference/sandbox` | `sandbox.json` schema |
| Cursor changelog | `https://cursor.com/changelog` | Per-release CLI changes |

## Third-party deep-dives (cross-references; not authoritative)

| Page | URL | Why useful |
|------|-----|------------|
| GitButler hooks deep-dive | `https://blog.gitbutler.com/cursor-hooks-deep-dive` | Walks the hook wire shape and gives example payloads not present in the official docs |
| tarq.net stream format | `https://tarq.net/posts/cursor-agent-stream-format/` | Documents the `tool_call` variants (`shellToolCall`, `readToolCall`, `editToolCall`, …) in the NDJSON stream |
| truefoundry MCP guide | `https://www.truefoundry.com/blog/mcp-servers-in-cursor-setup-configuration-and-security-guide` | Useful for MCP server transport types and the `cursor agent mcp` subcommand surface |

## Key inspection commands

| Inspection | Command |
|------------|---------|
| Subcommand surface | `cursor agent --help` (just the bottom-of-page command list) |
| MCP subcommands | `cursor agent mcp --help` |
| Approval flags | grep `--mode`/`--force`/`--yolo`/`--sandbox`/`--approve-mcps`/`--trust` in `cursor agent --help` |
| Worktree flags | grep `--worktree` / `--worktree-base` / `--skip-worktree-setup` |
| Output format flags | grep `--print` / `--output-format` / `--stream-partial-output` |

## testagent stub location

Once Phase 1 lands:
```sh
# The current cursor stub (read-only reference)
cat cmd/cursor/cursor.go
```

Phase 0 ships only this research skill — no `cmd/cursor/` source yet.

## Common pitfalls

- **Bare-invocation launches UI.** Never run `cursor agent` without `--help` during research. The bare command makes an API call (or shows a login prompt). All command-line probing goes via `--help` or explicit subcommand helps.
- **Version format change.** Cursor's CLI moved from semver (`3.2.16`) to a date-build identifier (`2026.05.09-0afadcc`). Cite the date tag verbatim; don't try to parse semver. The matrix header should match exactly what `cursor agent --version` prints.
- **Two MCP config locations.** Cursor reads BOTH `~/.cursor/mcp.json` (global) and `.cursor/mcp.json` (project-scoped). testagent will need to model the override precedence (project wins).
- **`hooks.json` 4-level cascade.** Enterprise > team > project > user. MVP covers project-level only; document the others as out-of-scope.
- **`type: "prompt"` hooks.** Cursor uniquely supports a `prompt` hook handler — natural-language text evaluated by the local LLM. testagent treats these as `accepted` (parsed, never fire). Same treatment codex's `prompt`/`agent` hook types get.
- **Permission wire is top-level.** Cursor returns `{"permission": "allow|deny|ask", "user_message": "...", "agent_message": "..."}` at the top of the JSON body, NOT inside a `hookSpecificOutput.decision.behavior` nest. The hookresult parser needs a third path for cursor.
- **`AGENTS.md` is read by Cursor too.** Same file codex reads, plain markdown. Cursor ALSO reads `.cursor/rules/*.mdc` (richer, with YAML frontmatter). Legacy `.cursorrules` is deprecated.
- **Plugin slash commands.** Plugins extend the slash surface via `skills/`. testagent treats all plugin-contributed slashes as `not relevant` — dynamic, depend on the installed plugin set.
- **`approvalMode` enum is under-documented.** Only `"allowlist"` confirmed in the wild. If you find more in docs (or in a fresh `cli-config.json`), add a row enumeration to the matrix.
- **No `cost_usd` in stream-json.** Cursor's `--print --output-format stream-json` does NOT include token cost fields the way Claude's does. Note this when documenting the stream format row.
- **`/mcp list` opens a browser UI.** Cursor's `/mcp list` is an interactive picker, not a list-and-exit command. Cite the docs page when adding the matrix row.
