---
name: research-codex-coverage
description: >
  Research the current Codex CLI release and update the compat matrix
  (COMPATIBILITY.md) with accurate flags, slash commands, and REPL behaviors.
when_to_use:
  - "update codex compat"
  - "check codex coverage"
  - "sync codex compatibility"
  - "new codex version"
  - "codex drift"
  - "research codex"
---

# research-codex-coverage

Use this skill to refresh the `## Codex` section of `COMPATIBILITY.md` after a Codex CLI release or when the compat matrix may have drifted from upstream.

## When to invoke

- A new Codex CLI version is released (`gh api "repos/openai/codex/releases?per_page=1" | jq -r '.[0].tag_name'`).
- `task dumpcli:codex` exits non-zero (flag drift detected).
- A contributor adds a new flag or command to testagent's codex stub and the matrix row is missing.
- Periodic coverage check (recommended: after each minor release series).

## Sources — read in this priority order

1. **Local binary** — `codex --help`, `codex --version`, and subcommand helps (`codex exec --help`, `codex review --help`, etc.). Source of truth for which flags exist in the installed version.
2. **Upstream slash-command source** — `codex-rs/tui/src/slash_command.rs` in the `openai/codex` repo. The `SlashCommand` enum is the authoritative list; doc comments in `description()` give the user-visible text.
3. **Config schema** — `codex-rs/core/config.schema.json` in the `openai/codex` repo. Authoritative for `config.toml` keys and the `HooksToml` event list.
4. **GitHub releases** — `gh api "repos/openai/codex/releases?per_page=5"` for version pin and recent changelog.
5. **testagent stub** — `cmd/codex/codex.go`. Currently recognizes `--session` and `--model` flags (mapped to Go vars but never used). Both are `accepted` in the matrix.

## Common pitfalls

- **`codex --help` is not exhaustive.** Subcommands have their own flags; run `codex <sub> --help` for each.
- **Config home is `~/.codex/`**, not `~/.config/codex/`. Config file is `~/.codex/config.toml`. Sessions are stored in `~/.codex/` as well. The environment variable `CODEX_HOME` overrides this.
- **`AGENTS.md` convention.** Codex reads `AGENTS.md` from the workspace root (and parent directories) for project-specific instructions. This is analogous to Claude Code's `CLAUDE.md`. Do not confuse it with testagent's own `AGENTS.md`.
- **MCP config is in `config.toml`** under `[mcp_servers]`, not a separate flag like Claude's `--mcp-config`. Codex also provides `codex mcp add/remove/list` subcommands for managing MCP servers.
- **Hooks are TOML-configured** under `[hooks]` in `config.toml`. The event names from `HooksToml` are: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `PreCompact`, `PostCompact`, `PermissionRequest`. Each event takes an array of `MatcherGroup` objects: `{matcher, hooks[]}`. Each entry in `hooks[]` has a `type` discriminator — `command`, `prompt`, or `agent` — plus type-specific fields (`command`, `prompt`, `agent` and the shared `timeout`/`async`). Do NOT model this as a flat `MatcherGroup{command, ...}` — that was a v0.2.0 bug fixed in #54 and the wrong shape will silently fail to decode real codex configs.
- **`--session` flag in the stub** is testagent-invented (not a real codex flag). The real codex uses `codex resume [SESSION_ID]` for resuming sessions. Document the stub flag as `accepted` (parsed by testagent, no codex equivalent).
- **`/exit` and `/quit` are aliases.** Both map to `SlashCommand::Exit`/`SlashCommand::Quit` respectively — they both exit Codex. testagent's `/exit` is compatible with either.
- **`/clear` vs `/new`.** Codex's `/clear` clears the terminal and starts a new chat; `/new` starts a new chat without clearing. These are different from Claude Code's `/clear` (which fires PreCompact/PostCompact).

## Naming-collision callout

**Always include this callout** in the slash-commands section when covering Codex:

> **Naming collision:** Codex's `/mcp` lists configured MCP tools (use `/mcp verbose` for details). testagent uses `/mcp-call` for tool dispatch to avoid colliding with this. Codex's `/exit` and `/quit` both exit the CLI; testagent's `/exit [code]` maps to `/exit`. Codex's `/clear` clears the terminal and starts a new chat; testagent's `/restart clear` simulates the hook side-effect. Codex's `/status` shows session config and token usage; testagent's `/restart` fires the analogous session lifecycle hooks.

## Issue-linking rule

Every `✗ planned` row **must** reference the tracking issue if one exists:

- General codex adapter work → [#13](https://github.com/paultyng/testagent/issues/13)
- Umbrella vendor-adapter tracker → [#14](https://github.com/paultyng/testagent/issues/14)
- If a conformance-suite issue exists (e.g., #15) → link it for test rows.
- If no issue covers a specific row → mark `Tracked in: TODO open issue` so a planning step can file follow-ups.

Do not file issues or post comments as part of this skill run.

## Output format

Produce or update the `## Codex` section of `COMPATIBILITY.md` at the repo root using the skeleton in `report-template.md`. Keep the `## Claude` section **untouched**. Hard cap: **1500 words** for the Codex section body (excluding legend and header).

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
1. **Built-in** — coded into the TUI (`SlashCommand` enum)
2. **Plugin/MCP-contributed** — dynamic service-tier commands; always `not relevant` for testagent

Note: Codex does not have a "bundled skill" category equivalent to Claude Code. Skills in Codex are user-installed via `/skills` and model-executed; mark the skills mechanism as `not relevant`.

### Scale targets (Codex vendor)

The matrix should cover approximately:
- ~15 flag rows (global flags + key subcommand flags; alphabetical by long name)
- ~30 slash-command rows (alphabetical within each group)
- ~8 REPL-behavior rows
- ~8 hook-event rows

## Hard constraint

Do not invent flag or command names. If a page is unreachable after one retry, leave the row as `| \`--flag\` | TODO: source | — |`. Do not file issues or post comments as part of this skill run.
