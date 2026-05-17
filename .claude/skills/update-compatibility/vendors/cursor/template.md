# COMPATIBILITY.md — Cursor section skeleton

Copy this as the `## Cursor` section into `COMPATIBILITY.md` after the `## Codex` section. Fill in each row.

---

<!-- BEGIN CURSOR TEMPLATE -->

## Cursor

**Upstream version researched:** Cursor CLI YYYY.MM.DD-<shortsha>
**Local binary version:** `cursor agent --version` → YYYY.MM.DD-<shortsha>

### Subcommands

Cursor's user-facing surface is divided across multiple subcommands under `cursor agent`; the bare `cursor agent [prompt...]` form is the interactive REPL. This section tracks subcommand coverage first, then per-subcommand flag tables for those with non-trivial flag surfaces.

| Subcommand | testagent | Notes |
|------------|-----------|-------|
| `cursor agent` (no subcommand) | `✗ planned` | Interactive session; tracked in cursor adapter umbrella issue |
| `cursor agent --print` | `✗ planned` | Non-interactive one-shot; `--output-format text\|json\|stream-json` |
| `cursor agent mcp list` | `✗ planned` | Interactive MCP browser; testagent renders a flat listing |
| `cursor agent mcp list-tools <identifier>` | `✗ planned` | List tools per server |
| `cursor agent mcp enable <identifier>` | `✗ planned` | Add to local approved list |
| `cursor agent mcp disable <identifier>` | `✗ planned` | Disable server |
| `cursor agent mcp login <identifier>` | `✗ planned` | OAuth handshake; stub returns canned success |
| `cursor agent login` | `✗ planned` | Auth no-op stub |
| `cursor agent logout` | `✗ planned` | Auth no-op stub |
| `cursor agent status` / `whoami` | `✗ planned` | Returns canned auth status; `--format text\|json` |
| `cursor agent about` | `✗ planned` | Returns canned version info; `--format text\|json` |
| `cursor agent models` | `✗ planned` | Returns canned model list |
| `cursor agent update` | `not relevant` | Self-update; testagent has its own release cadence |
| `cursor agent create-chat` | `✗ planned` | Returns a generated chat ID |
| `cursor agent generate-rule` | `not relevant` | Interactive rule-authoring wizard; requires model |
| `cursor agent ls` | `✗ planned` | Resume picker; stub returns empty list |
| `cursor agent resume` | `✗ planned` | Resume latest; stub returns "no session" |
| `cursor agent install-shell-integration` | `not relevant` | Writes to `~/.zshrc`; not in scope for a fake agent |
| `cursor agent uninstall-shell-integration` | `not relevant` | Symmetric to above |

### Flags (global / interactive)

Alphabetical by long name. Short flags shown inline.

| Flag | testagent | Notes |
|------|-----------|-------|
| `--api-key <key>` | `accepted` | Auth key; parsed but unused (testagent has no upstream API) |
| `--approve-mcps` | `accepted` | Auto-approve MCP servers; parsed but no permission system |
| `--continue` | `✗ planned` | Continue previous session |
| `-f` / `--force` / `--yolo` | `accepted` | Approval bypass; parsed but no approval system |
| `-H` / `--header <header>` | `accepted` | Custom HTTP header; parsed but no model |
| `--list-models` | `✗ planned` | Print model list and exit |
| `-m` / `--model <name>` | `accepted` | Model name; no model |
| `--mode <plan\|ask>` | `✗ planned` | Switch to plan / Q&A mode at start |
| `--output-format <text\|json\|stream-json>` | `✗ planned` | Output formatter for `--print` |
| `-p` / `--print` | `✗ planned` | Non-interactive one-shot |
| `--plan` | `✗ planned` | Shorthand for `--mode=plan` |
| `--plugin-dir <path>` | `accepted` | Load local plugin dir; parsed but no plugin system |
| `--resume [chatId]` | `✗ planned` | Resume a session by ID |
| `--sandbox <enabled\|disabled>` | `accepted` | Sandbox override; no sandbox |
| `--skip-worktree-setup` | `accepted` | Skip worktree setup scripts; no worktree integration |
| `--stream-partial-output` | `✗ planned` | Token-level deltas in `stream-json`; only with `--print` |
| `--trust` | `accepted` | Trust workspace without prompting (with `--print`) |
| `--workspace <path>` | `✗ planned` | Workspace dir override (analog of `--cd`) |
| `-w` / `--worktree [name]` | `accepted` | Worktree creation; parsed but no worktree management |
| `--worktree-base <branch>` | `accepted` | Worktree base ref |

### Slash commands

> **Naming collision:** Cursor's `/mcp list` opens an interactive MCP browser. testagent uses `/mcp-call` for tool dispatch to avoid the collision. Cursor has no top-level `/exit` slash — Ctrl+C exits.

#### Built-in

Alphabetical.

| Command | testagent | Notes |
|---------|-----------|-------|
| `/about` | `✗ planned` | Environment info |
| `/ask` | `✗ planned` | Switch to Q&A mode (read-only) |
| `/compress` | `✗ planned` | Reduce context window usage |
| `/mcp list` | `not relevant` | Interactive MCP browser; testagent uses `/mcp-call` |
| `/model` | `not relevant` | Model selector; no model |
| `/plan` | `✗ planned` | Switch to plan mode (read-only/planning) |
| `/resume` | `✗ planned` | Continue prior conversation |
| `/setup-terminal` | `not relevant` | Configures keybindings on host shell; TUI-internal |
| `/usage` | `not relevant` | Usage statistics; requires upstream API |

#### Plugin-contributed (skills)

| Pattern | testagent | Notes |
|---------|-----------|-------|
| Plugin-contributed slashes | `not relevant` | Dynamic; depend on installed plugins; no plugin system |

### REPL behaviors

| Behavior | testagent | Notes |
|----------|-----------|-------|
| `Ctrl+C` (exit) | `✗ planned` | Cursor has no `/exit` slash; Ctrl+C is the documented exit |
| Inline `--print` deltas (`--stream-partial-output`) | `✗ planned` | NDJSON text-deltas in stream-json |
| Alternate screen mode | `not relevant` | TUI-internal |
| Vim composer mode | `not relevant` | TUI-internal |

### Hook events

Hooks are configured in `.cursor/hooks.json` (project-scoped) with a 4-level priority cascade (enterprise > team > project > user). testagent's MVP covers project-level only; document the others as out-of-scope. Each hook entry is `{"type": "command"\|"prompt", "command": "..."}` plus optional fields. The response wire is **top-level** `{"permission": "allow"\|"deny"\|"ask", "user_message": "...", "agent_message": "..."}` — distinct from claude/codex's nested shape.

| Event | testagent | Notes |
|-------|-----------|-------|
| `sessionStart` | `✗ planned` | Fires when a session begins |
| `sessionEnd` | `✗ planned` | Fires when a session terminates |
| `workspaceOpen` | `not relevant` | App-lifecycle event; no app shell in testagent |
| `beforeSubmitPrompt` | `✗ planned` | Before the model receives the user prompt |
| `afterAgentResponse` | `✗ planned` | After Cursor finishes a response |
| `afterAgentThought` | `not relevant` | Tied to model internals |
| `preToolUse` | `✗ planned` | Before any tool call; can allow/deny/ask |
| `postToolUse` | `✗ planned` | After a tool call succeeds |
| `postToolUseFailure` | `✗ planned` | After a tool call fails |
| `beforeShellExecution` | `✗ planned` | Before a shell command runs |
| `afterShellExecution` | `✗ planned` | After a shell command runs |
| `beforeMCPExecution` | `✗ planned` | Before an MCP tool call |
| `afterMCPExecution` | `✗ planned` | After an MCP tool call |
| `beforeReadFile` | `✗ planned` | Before a file read; can block at runtime |
| `afterFileEdit` | `✗ planned` | After a file edit lands |
| `subagentStart` | `✗ planned` | Subagent spawned |
| `subagentStop` | `✗ planned` | Subagent finished |
| `taskCreated` | `✗ planned` | Task created via internal task system |
| `taskCompleted` | `✗ planned` | Task marked complete |
| `preCompact` | `✗ planned` | Before context compaction |
| `stop` | `✗ planned` | Cursor's analog of claude's Stop |
| `beforeTabFileRead` | `not relevant` | IDE-tab event; no IDE integration |
| `afterTabFileEdit` | `not relevant` | IDE-tab event; no IDE integration |

### Config and conventions

| Feature | testagent | Notes |
|---------|-----------|-------|
| `.cursor/hooks.json` (project) | `✗ planned` | Project-scoped hook config; MVP covers this level only |
| `~/.cursor/hooks.json` (user) | `not relevant` | User-scoped tier of the priority cascade; out of scope for MVP |
| Team-scoped hooks | `not relevant` | Distributed via Cursor dashboard; no central server |
| Enterprise-scoped hooks | `not relevant` | macOS: `/Library/Application Support/Cursor/hooks.json`; out of scope |
| `~/.cursor/mcp.json` (global) | `✗ planned` | Identical shape to claude's `--mcp-config` |
| `.cursor/mcp.json` (project) | `✗ planned` | Project override of global MCP config |
| `cursor agent mcp` subcommands | `✗ planned` | Read/list/enable/disable surface (codex has add/remove/list — different) |
| `~/.cursor/cli-config.json` | `accepted` | Parsed for `approvalMode`, sandbox, permissions; not enforced |
| `approvalMode` token grammar | `not relevant` | Allowlist tokens (`Shell(...)`, `Read(...)`, `Mcp(server:tool)`); no permission engine |
| `sandbox.json` | `not relevant` | Sandbox mode picker; no sandbox |
| `AGENTS.md` project instructions | `✗ planned` | Same file codex reads; surface in banner/status |
| `.cursor/rules/*.mdc` | `✗ planned` | Richer rule format with YAML frontmatter (`description`/`alwaysApply`/`globs`) |
| `.cursorrules` (legacy) | `not relevant` | Deprecated; not loaded by current agent CLI |
| Plugins (`~/.cursor/plugins/local/<name>`) | `not relevant` | Dynamic; depend on installed plugin set |
| `--plugin-dir <path>` | `accepted` | Local plugin discovery; no plugin engine |
| Worktree integration (`~/.cursor/worktrees/<repo>/<name>`) | `✗ planned` | First-class via `--worktree`/`--worktree-base` |

<!-- END CURSOR TEMPLATE -->
