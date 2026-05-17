---
name: research-cursor-coverage
description: Use when refreshing the `## Cursor` section of COMPATIBILITY.md after a Cursor CLI release, when `task dumpcli:cursor` reports drift, when a contributor adds a flag or slash command not yet in the matrix, or for periodic coverage checks. Triggers include "update cursor compat", "check cursor coverage", "sync cursor compatibility", "new cursor version", "cursor drift", "research cursor".
---

# research-cursor-coverage

Use this skill to refresh the `## Cursor` section of `COMPATIBILITY.md` after a Cursor CLI release or when the compat matrix may have drifted from upstream.

Cursor is the third vendor adapter alongside claude and codex. Its surface differs notably:

- **Hook events**: ~21 events (vs. claude's 10 / codex's 8); includes file-level (`beforeReadFile`, `afterFileEdit`), shell-specific, MCP-specific, subagent, and the rest of the lifecycle.
- **Hook config**: `.cursor/hooks.json` with a 4-level priority cascade (enterprise > team > project > user). MVP covers project-level only.
- **Hook handler types**: `command` and **`prompt`** (LLM-evaluated natural language — accepted-but-not-fired in testagent).
- **Permission wire**: top-level `{permission: "allow|deny|ask", user_message, agent_message}` — a third wire shape distinct from claude/codex's nested `decision.behavior`.
- **Plugins**: directory-based; can contribute rules, skills, agents, commands, MCP servers, hooks. No claude/codex equivalent.
- **Worktree integration**: first-class via `--worktree`, `--worktree-base`, `~/.cursor/worktrees/`.

## When to invoke

- A new Cursor CLI version is released (`cursor agent --version` shifts).
- `task dumpcli:cursor` exits non-zero (flag drift detected) — once that target exists.
- A contributor adds a new flag or command to testagent's cursor stub and the matrix row is missing.
- Periodic coverage check (recommended: after each Cursor monthly changelog).

## Sources — read in this priority order

1. **Local binary** — `cursor agent --help`, `cursor agent --version`, and per-subcommand helps (`cursor agent mcp --help`, `cursor agent status --help`, etc.). Source of truth for which flags exist in the installed version. **Do NOT invoke `cursor agent` interactively** — the bare command launches the agent UI / makes an API call. Only `--help` and explicit subcommand helps are safe.
2. `compat/cursor.cli.txt` — committed contract artifact (once `task dumpcli:cursor` lands). Authoritative for which testagent flags exist.
3. `COMPATIBILITY.md` — current state of record; start here to avoid re-researching unchanged rows.
4. **Upstream docs** at `https://cursor.com/docs/`:
   - `cli/using` — interactive REPL, slash commands, shortcuts.
   - `cli/reference/output-format` — `--print` stream-json shape (NDJSON `tool_call started/completed` per-tool variants).
   - `cli/reference/permissions` — `approvalMode`, allowlist token grammar (`Shell(...)`, `Read(...)`, `Mcp(server:tool)`).
   - `hooks` — hook event reference, wire shape, priority cascade.
   - `rules` — `.cursor/rules/*.mdc` frontmatter (`description`, `alwaysApply`, `globs`).
   - `plugins` — plugin directory layout, manifest, component discovery.
   - `reference/sandbox` — `sandbox.json` schema (`workspace_readwrite` / `workspace_readonly` / `insecure_none`).
5. `changelog/cli-*` — recent CLI changelog entries flag new/removed flags between versions.
6. **Cursor changelog and release notes** at `https://cursor.com/changelog`.

## Common pitfalls

- **Interactive launch.** `cursor agent` with no args launches the agent. Always probe via `--help` or subcommand `--help`. Never run interactive in CI or research scripts.
- **Version format.** Cursor's CLI version shifted from semver (`3.2.16`) to a date-based identifier (`2026.05.09-0afadcc`). When updating the matrix, cite the date-tag verbatim — don't try to parse semver.
- **MCP config shape == claude's.** `~/.cursor/mcp.json` and `.cursor/mcp.json` both use `{"mcpServers": {<name>: {...}}}` — identical wrapper to claude. testagent can reuse `internal/mcp`'s types. Servers can be stdio / SSE / streamable-HTTP.
- **MCP subcommands differ from codex.** Cursor exposes `cursor agent mcp login|list|list-tools|enable|disable`. The shape is read/enable/disable, not the CRUD `add/remove/list` codex offers.
- **`type: "prompt"` hooks are NL.** Cursor's hook handler can be a plain-text "prompt" string evaluated by the local LLM. testagent's value prop is no-LLM, so prompt hooks are `accepted` (parsed, never fire) — same treatment codex's `prompt`/`agent` hook types get.
- **Permission wire is top-level, not nested.** Cursor hooks return `{"permission": "allow|deny|ask", "user_message": "...", "agent_message": "..."}` at the top of the JSON, not inside a `hookSpecificOutput.decision.behavior` nest. The hookresult parser needs a third path.
- **`permission: "ask"`** — same semantics as claude's PreToolUse `ask`. Pair with a cursor variant of `/fake-permission-resolve`.
- **`approvalMode` values.** Only `"allowlist"` is observed in the wild today; docs may not enumerate the full set. Note any other values seen + cite the doc URL.
- **Rules file dual format.** Cursor reads BOTH `AGENTS.md` (plain markdown, codex-shape) AND `.cursor/rules/*.mdc` (richer, with YAML frontmatter). The `.cursorrules` legacy file is deprecated; do not list as supported.
- **Plugins discovery.** A plugin is a directory under `~/.cursor/plugins/local/<name>` (or distributed via marketplace). Each can contribute rules/, skills/, agents/, commands/, mcp.json, hooks/. The `--plugin-dir` flag adds a local path.
- **Slash-command set is small.** Only ~9 documented (`/plan`, `/ask`, `/compress`, `/resume`, `/model`, `/usage`, `/about`, `/setup-terminal`, `/mcp list`). Plugins extend it dynamically.

## Naming-collision callouts

**Always include this callout** in the slash-commands section when covering Cursor:

> **Naming collision:** Cursor's `/mcp list` opens an interactive MCP browser. testagent uses `/mcp-call` for tool dispatch to avoid the collision. Cursor has no top-level `/exit` slash — Ctrl+C exits.

## Issue-linking rule

Every `✗ planned` row **must** reference the tracking issue if one exists:

- General cursor adapter work → cursor-adapter umbrella issue (file when Phase 1 starts).
- Umbrella vendor-adapter tracker → [#14](https://github.com/paultyng/testagent/issues/14)
- If no issue covers a specific row → mark `Tracked in: TODO open issue` so a planning step can file follow-ups.

Do not file issues or post comments as part of this skill run.

## Output format

Produce or update the `## Cursor` section of `COMPATIBILITY.md` at the repo root using the skeleton in `report-template.md`. Keep the `## Claude` and `## Codex` sections **untouched**. Hard cap: **1500 words** for the Cursor section body (excluding legend and header).

### Five-state legend

| Symbol | Meaning |
|--------|---------|
| `✓ supported` | testagent implements this feature |
| `partial` | partially implemented (note what's missing) |
| `accepted` | flag/command accepted without error but silently ignored |
| `not relevant` | not applicable to a fake agent (e.g. TUI-internal, model-dependent) |
| `✗ planned` | not yet implemented; tracked in an issue |

### Grouping for slash commands

Group slash-command rows by origin:
1. **Built-in** — coded into the agent (`/plan`, `/ask`, etc.)
2. **Plugin-contributed** — dynamic; depends on installed plugins; always `not relevant` for testagent
3. **Skill-invoked** — slash commands provided by plugins' `skills/` directories; always `not relevant`

### Scale targets (Cursor vendor)

The matrix should cover approximately:
- ~20 flag rows (`agent` subcommand flags; alphabetical by long name)
- ~10 slash-command rows (small set; alphabetical)
- ~10 REPL-behavior rows
- ~21 hook-event rows (the full Cursor set)
- ~12 subcommand rows (top-level `cursor agent <sub>`)
- ~6 config rows (`approvalMode`, `sandbox`, MCP, rules, plugins, worktree)

## Hard constraint

Do not invent flag or command names. If a page is unreachable after one retry, leave the row as `| \`--flag\` | TODO: source | — |`. Do not file issues or post comments as part of this skill run. Do not invoke `cursor agent` without `--help` — interactive mode launches the agent.
