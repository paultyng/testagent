# COMPATIBILITY.md — Codex section skeleton

Copy this as the `## Codex` section into `COMPATIBILITY.md` after the `## Claude` section. Fill in each row.

---

<!-- BEGIN CODEX TEMPLATE -->

## Codex

**Upstream version researched:** codex-cli vX.Y.Z (YYYY-MM-DD) — tag `rust-vX.Y.Z`
**Local binary version:** `codex --version` → codex-cli X.Y.Z

### Flags

Alphabetical by long name. Short flags shown inline. Global flags apply to interactive mode and most subcommands; subcommand-specific flags are noted.

| Flag | testagent | Notes |
|------|-----------|-------|
| `--add-dir` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `--ask-for-approval` / `-a` | `not relevant` | Approval policy; no execution engine |
| `--cd` / `-C` | `✗ planned` | Working root override; tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `--config` / `-c` | `not relevant` | Runtime config override; no config system in stub |
| `--dangerously-bypass-approvals-and-sandbox` | `not relevant` | No sandbox in testagent |
| `--disable` | `not relevant` | Feature flag toggle; no feature system |
| `--enable` | `not relevant` | Feature flag toggle; no feature system |
| `--image` / `-i` | `not relevant` | Image attachment; no model |
| `--local-provider` | `not relevant` | OSS provider selection; no model |
| `--model` / `-m` | `accepted` | Parsed by stub; silently ignored |
| `--no-alt-screen` | `not relevant` | TUI-internal |
| `--oss` | `not relevant` | Open-source provider flag; no model |
| `--profile` / `-p` | `not relevant` | Config profile selection; no config system |
| `--remote` | `not relevant` | Remote app-server websocket; not applicable |
| `--remote-auth-token-env` | `not relevant` | Remote auth; not applicable |
| `--sandbox` / `-s` | `not relevant` | Sandbox policy; no execution engine |
| `--search` | `not relevant` | Web search tool; no model |
| `--session` | `accepted` | Testagent-invented stub flag; no real codex equivalent (real resume is `codex resume` subcommand) |
| `--version` / `-V` | `not relevant` | testagent uses its own `--version` |
| <!-- ADD ROWS --> | | |

### Slash commands

> **Naming collision:** Codex's `/mcp` lists configured MCP tools (use `/mcp verbose` for details). testagent uses `/mcp-call` for tool dispatch to avoid colliding with this. Codex's `/exit` and `/quit` both exit the CLI; testagent's `/exit [code]` maps to `/exit`. Codex's `/clear` clears the terminal and starts a new chat; testagent's `/restart clear` simulates the hook side-effect. Codex's `/status` shows session config and token usage; testagent's `/restart` fires the analogous session lifecycle hooks.

#### Built-in

Alphabetical. All `✗ planned` rows tracked in [#13](https://github.com/paultyng/testagent/issues/13) unless noted.

| Command | testagent | Notes |
|---------|-----------|-------|
| `/agent` | `✗ planned` | Switch active agent thread |
| `/apps` | `not relevant` | App/connector management; no connector system |
| `/approve` | `not relevant` | Auto-review approval; no approval system |
| `/clear` | `✗ planned` | Clears terminal, starts new chat; `/restart clear` fires the hook side-effect only |
| `/collab` | `not relevant` | Collaboration mode; experimental feature |
| `/compact` | `✗ planned` | Context summarization; tracked in [#12](https://github.com/paultyng/testagent/issues/12) |
| `/copy` | `not relevant` | Copy last response to clipboard; TUI-internal |
| `/debug-config` | `not relevant` | Config layer debug; no config system |
| `/diff` | `not relevant` | Git diff display; TUI-internal |
| `/exit` | `✗ planned` | Exit Codex (alias of `/quit`); tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `/experimental` | `not relevant` | Feature toggle; no feature system |
| `/feedback` | `not relevant` | Send logs to maintainers; TUI-internal |
| `/fork` | `✗ planned` | Fork current chat session |
| `/goal` | `not relevant` | Set/view long-running task goal; requires model |
| `/hooks` | `not relevant` | View and manage lifecycle hooks via TUI; no TUI hook browser |
| `/ide` | `not relevant` | IDE context injection; no IDE integration |
| `/init` | `not relevant` | Create AGENTS.md file; requires model |
| `/keymap` | `not relevant` | Remap TUI shortcuts; TUI-internal |
| `/logout` | `not relevant` | Log out of Codex; no auth system |
| `/mcp` | `not relevant` | List MCP tools; testagent uses `/mcp-call` instead |
| `/memories` | `not relevant` | Memory configuration; requires model |
| `/mention` | `not relevant` | File mention/autocomplete; TUI-internal |
| `/model` | `not relevant` | Model and reasoning effort picker; no model |
| `/new` | `✗ planned` | Start new chat without clearing terminal |
| `/permissions` | `not relevant` | Permission management; no permission system |
| `/personality` | `not relevant` | Communication style picker; requires model |
| `/plan` | `not relevant` | Switch to plan mode; requires model |
| `/plugins` | `not relevant` | Plugin browser; no plugin system |
| `/ps` | `not relevant` | List background terminals; TUI-internal |
| `/quit` | `✗ planned` | Exit Codex; tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `/raw` | `not relevant` | Toggle raw scrollback mode; TUI-internal |
| `/realtime` | `not relevant` | Toggle realtime voice mode; experimental |
| `/rename` | `✗ planned` | Rename current thread |
| `/resume` | `✗ planned` | Resume a saved chat session |
| `/review` | `not relevant` | Code review; requires model |
| `/sandbox-add-read-dir` | `not relevant` | Sandbox read-dir addition; no sandbox |
| `/settings` | `not relevant` | Realtime microphone/speaker config; TUI-internal |
| `/setup-default-sandbox` | `not relevant` | Elevated sandbox setup; no sandbox |
| `/side` | `not relevant` | Start ephemeral side conversation; requires model |
| `/skills` | `not relevant` | Skill browser and installation; requires model |
| `/status` | `not relevant` | Session config and token usage; no session state |
| `/statusline` | `not relevant` | Status line configuration; TUI-internal |
| `/stop` | `not relevant` | Stop all background terminals; TUI-internal |
| `/subagents` | `not relevant` | Switch active agent thread (alias of `/agent`) |
| `/theme` | `not relevant` | Syntax highlighting theme picker; TUI-internal |
| `/title` | `not relevant` | Terminal title configuration; TUI-internal |
| `/vim` | `not relevant` | Toggle Vim composer mode; TUI-internal |
| <!-- ADD ROWS --> | | |

#### Plugin/MCP-contributed (service-tier)

| Pattern | testagent | Notes |
|---------|-----------|-------|
| Service-tier commands | `not relevant` | Dynamic; contributed by connected MCP servers; no model |

### REPL behaviors

| Behavior | testagent | Notes |
|----------|-----------|-------|
| `!`-shell prefix | `not relevant` | Spawns a shell command in the composer; TUI-internal |
| `Ctrl+C` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `Ctrl+D` / EOF | `not relevant` | TUI-internal |
| `Esc` (cancel in-flight turn) | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| External editor (`open_external_editor` action) | `not relevant` | TUI-internal |
| Transcript overlay (`open_transcript` action) | `not relevant` | TUI-internal |
| Vim composer mode (`/vim`) | `not relevant` | TUI-internal |
| Alternate screen mode (`--no-alt-screen`) | `not relevant` | TUI-internal |
| <!-- ADD ROWS --> | | |

### Hook events

Hooks are configured in `~/.codex/config.toml` under `[hooks]`. Each event takes an array of `MatcherGroup` objects: `{matcher: string, hooks: []Hook}`. Each `Hook` carries a `type` discriminator (`command`, `prompt`, or `agent`) plus type-specific fields.

| Event | testagent | Notes |
|-------|-----------|-------|
| `SessionStart` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `UserPromptSubmit` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `PreToolUse` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `PostToolUse` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `Stop` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `PreCompact` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `PostCompact` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `PermissionRequest` | `not relevant` | Approval/permission system; no permission system in testagent |
| <!-- ADD ROWS --> | | |

### Config and conventions

| Feature | testagent | Notes |
|---------|-----------|-------|
| `~/.codex/config.toml` | `not relevant` | Codex config home; testagent has no config-loading from this path |
| `CODEX_HOME` env override | `not relevant` | Overrides `~/.codex/`; not applicable |
| `AGENTS.md` project instructions | `not relevant` | Read by the model at session start; testagent has no model |
| `[mcp_servers]` in config.toml | `not relevant` | MCP servers configured via TOML; testagent uses `--mcp-config` flag (Claude shape) |
| `codex mcp add/remove/list` | `not relevant` | Subcommands to manage MCP config; no config management in stub |

<!-- END CODEX TEMPLATE -->
