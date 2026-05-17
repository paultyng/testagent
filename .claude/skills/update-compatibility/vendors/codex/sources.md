# Sources — codex vendor

## Local binary recipes

```sh
# Version (also sets the "Upstream version researched" header)
codex --version

# Root help (global flags)
codex --help

# Subcommand helps
codex exec --help
codex review --help
codex resume --help
codex fork --help
codex mcp --help
codex login --help
codex plugin --help
codex sandbox --help
codex apply --help
```

## gh API recipes

**Pin every `gh api contents/...` recipe to a release tag** so the snapshot matches the version cited at the top of `COMPATIBILITY.md`. Default for this matrix revision: `rust-v0.130.0`. When refreshing for a new release, bump `REF` AND the version cite at the top of COMPATIBILITY.md in the same commit — drift between the two is what #55 fixed.

```sh
REF=rust-v0.130.0

# Latest stable release tags (no ref pin — this IS the picker)
gh api "repos/openai/codex/releases?per_page=5" | jq '[.[] | {tag_name, published_at, name}]'

# Slash-command source of truth (authoritative enum)
gh api "repos/openai/codex/contents/codex-rs/tui/src/slash_command.rs?ref=$REF" | jq -r '.content' | base64 -d

# Hook event list + config schema
gh api "repos/openai/codex/contents/codex-rs/core/config.schema.json?ref=$REF" | jq -r '.content' | base64 -d | jq '.definitions.HooksToml'

# Full config.toml key inventory
gh api "repos/openai/codex/contents/codex-rs/core/config.schema.json?ref=$REF" | jq -r '.content' | base64 -d | jq '.properties | keys'

# Example config.toml from the repo's own CI environment
gh api "repos/openai/codex/contents/.github/codex/home/config.toml?ref=$REF" | jq -r '.content' | base64 -d

# Keymap actions (REPL behaviors)
gh api "repos/openai/codex/contents/codex-rs/tui/src/keymap_setup/actions.rs?ref=$REF" | jq -r '.content' | base64 -d
```

## Upstream doc URLs

| Page | URL | Status |
|------|-----|--------|
| openai/codex repo README | `https://github.com/openai/codex/blob/main/README.md` | Available |
| codex-rs README | `https://github.com/openai/codex/blob/main/codex-rs/README.md` | Available |
| TUI styles guide | `https://github.com/openai/codex/blob/main/codex-rs/tui/styles.md` | TODO: verify |
| Official OpenAI docs (if exists) | `https://platform.openai.com/docs/codex` | TODO: verify |

## Key source files in openai/codex

| File | What it provides |
|------|-----------------|
| `codex-rs/tui/src/slash_command.rs` | Authoritative slash-command list (`SlashCommand` enum + descriptions) |
| `codex-rs/core/config.schema.json` | Full config.toml schema; `HooksToml` definition has all hook event names |
| `codex-rs/tui/src/keymap_setup/actions.rs` | All configurable TUI key actions (REPL behaviors) |
| `codex-rs/tui/src/bottom_pane/slash_commands.rs` | Slash-command filtering/gating logic |
| `.github/codex/home/config.toml` | Minimal example `config.toml` from the repo itself |
| `AGENTS.md` | Repo-level instructions Codex reads as project context |

## testagent stub location

```sh
# The current codex stub (read-only reference)
cat cmd/codex/codex.go
```

Key facts from the stub:
- Registers `--session` flag (string, empty default) — testagent-invented, no codex equivalent
- Registers `--model` flag (string, empty default) — maps to real codex `-m/--model`
- Both are bound to local Go vars and never used; `RunE` just prints a "not yet implemented" message

## Common pitfalls

- **Config home.** `~/.codex/` is the config home (overridden by `$CODEX_HOME`). The file is `config.toml`, not YAML. Sessions, logs, and auth are also stored here.
- **`AGENTS.md` vs `CLAUDE.md`.** Codex reads `AGENTS.md`; Claude Code reads `CLAUDE.md`. testagent's own `AGENTS.md` is for testagent development conventions, not the emulated codex behavior.
- **MCP config location.** Codex MCP servers live in `config.toml` under `[mcp_servers]`, not a CLI flag. The `codex mcp add/remove/list` subcommands manage this section. Claude uses `--mcp-config <json-file>`.
- **Hook handler shape.** Codex hooks take a `command` string (shell command) with optional `async`, `timeout`, and `statusMessage`. The matcher also supports a `patterns` array to filter by tool name etc. This differs from Claude's HTTP POST hook shape.
- **`--session` is not a real Codex flag.** The stub flag name was invented by testagent. Real session resume is `codex resume [SESSION_ID]` (subcommand), not a flag.
- **Slash-command enum is display-order, not alpha.** The `SlashCommand` enum is ordered by frequency-of-use for the TUI popup. When writing the matrix, re-sort alphabetically.
- **`/clean` is an alias for `/stop`.** Do not list it as a separate command.
- **Debug commands `debug-m-drop` / `debug-m-update`.** These are not user-facing (description says "DO NOT USE"); omit from the matrix or mark `not relevant`.
