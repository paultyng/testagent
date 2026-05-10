# Compatibility

testagent emulates real CLI agents. This matrix tracks which upstream features are implemented, accepted (parsed but no-op), or intentionally absent.

**Upstream version researched:** Claude Code v2.1.138 (2026-05-09)
**Local binary version:** `claude --version` — 2.1.126

## Legend

| Symbol | Meaning |
|--------|---------|
| `✓ supported` | testagent implements this feature |
| `partial` | partially implemented (note in column) |
| `accepted` | flag accepted without error, silently ignored |
| `not relevant` | not applicable to a fake agent |
| `✗ planned` | not yet implemented; tracked in issue |

---

## Claude

### Flags

Alphabetical by long name. Short flags shown inline. Global flags (common across all subcommands) are marked *(global)*.

| Flag | testagent | Notes |
|------|-----------|-------|
| `--add-dir` | ✓ supported | Repeatable; stored, shown in status line |
| `--agent` | `not relevant` | No agent routing in testagent |
| `--agents` | `not relevant` | No model or subagent support |
| `--append-system-prompt` | accepted | Displayed in loaded-status line; not used |
| `--append-system-prompt-file` | `✗ planned` | |
| `--auto-exit` *(global)* | ✓ supported | Auto-exits after duration; 0 = disabled |
| `--bare` | `not relevant` | No discovery phase in testagent |
| `--continue` / `-c` | `✗ planned` | |
| `--dangerously-skip-permissions` | `not relevant` | No permission system |
| `--debug` | `not relevant` | No internal debug subsystem |
| `--disable-slash-commands` | `not relevant` | Slash grammar is always active |
| `--effort` | `not relevant` | No model |
| `--exit-after` *(global)* | ✓ supported | Auto-exits after N interactions |
| `--history-cap` *(global)* | ✓ supported | TUI scrollback cap (default 1000; 0 = unlimited) |
| `--mcp-config` | ✓ supported | Connects, handshakes, exposes tools via `/mcp-call` |
| `--model` | `not relevant` | No model in testagent |
| `--name` / `-n` | ✓ supported | Shown in banner (default `test-agent`) |
| `--output-format` | ✓ supported | `text`, `json`, `stream-json`; used with `--print` |
| `--permission-mode` | `not relevant` | No permission system |
| `--print` / `-p` | ✓ supported | Non-interactive one-shot mode |
| `--resume` / `-r` | ✓ supported | Sets session ID; fires `source=resume` on `SessionStart` |
| `--session-id` | ✓ supported | UUID for the session |
| `--settings` | ✓ supported | Claude-shaped JSON; loads hook URLs |
| `--stream-delay` *(global)* | ✓ supported | Per-token stream interval (default 30ms) |
| `--system-prompt` | `not relevant` | No model to prompt |
| `--system-prompt-file` | `not relevant` | No model to prompt |
| `--think-delay` *(global)* | ✓ supported | Thinking-spinner duration (default 2s) |
| `--verbose` *(global)* | ✓ supported | Logs hook POSTs to stderr (no `-v` short — that's `--version`) |
| `--version` / `-v` *(global)* | ✓ supported | Prints `testagent version <X.Y.Z>`; injected at build time via `-ldflags`, falls back to `dev` |
| `--worktree` / `-w` | `not relevant` | No git worktree management |

### Slash commands

> **Naming collision:** Claude Code's `/mcp` opens a server-management UI. testagent uses `/mcp-call` for tool dispatch to avoid colliding with this. Claude Code's `/exit` exits the CLI; testagent's `/exit [code]` does the same. Claude Code's `/help` shows help; testagent's `/help` does the same.

#### Built-in

| Command | testagent | Notes |
|---------|-----------|-------|
| `/add-dir <path>` | `✗ planned` | |
| `/clear` | `✗ planned` | `/restart clear` simulates the hook side-effect |
| `/compact` | `✗ planned` | `/restart compact` simulates the hook side-effect; full support tracked in [#12](https://github.com/paultyng/testagent/issues/12) |
| `/config` | `not relevant` | No settings UI |
| `/context` | `not relevant` | No context window |
| `/exit` | ✓ supported | Accepts optional exit code |
| `/help` | ✓ supported | Lists testagent slash commands |
| `/mcp` | `not relevant` | testagent uses `/mcp-call` instead |
| `/model` | `not relevant` | No model |
| `/permissions` | `not relevant` | No permission system |
| `/resume` | `not relevant` | Session resume is flag-only in testagent |
| `/status` | `not relevant` | No settings UI |

#### Bundled skill

Bundled skills always land at `not relevant` — testagent has no model.

| Command | testagent | Notes |
|---------|-----------|-------|
| `/batch` | `not relevant` | Requires model |
| `/claude-api` | `not relevant` | Requires model |
| `/debug` | `not relevant` | Requires model |
| `/fewer-permission-prompts` | `not relevant` | Requires model |
| `/loop` | `not relevant` | Requires model |
| `/simplify` | `not relevant` | Requires model |

#### Testagent-only (no upstream equivalent)

| Command | Purpose |
|---------|---------|
| `/fake-tool <name> <json-args>` | Renders a fake tool-use block; pair with `/fake-tool-result` to fire `PostToolUse` |
| `/fake-tool-result <json-or-text>` | Completes a pending `/fake-tool`; fires `PostToolUse` hook |
| `/link <url> [text]` | Renders an OSC 8 hyperlink (clickable in supporting terminals); text defaults to URL |
| `/mcp-call <server.tool> <json-args>` | Calls a connected MCP tool; named to avoid `/mcp` collision |
| `/panel <text>` | Renders text in a rounded-border box |
| `/restart [clear\|compact]` | Fires `SessionEnd` then `SessionStart`; simulates `/clear` or `/compact` hook behavior |
| `/stream <duration> <message>` | Routes message through prompt path with per-token interval overridden |
| `/think <duration> <message>` | Routes message through prompt path with thinking-spinner duration overridden |

#### Plugin/MCP-contributed

| Pattern | testagent | Notes |
|---------|-----------|-------|
| `/mcp__<server>__<prompt>` | `not relevant` | Dynamic; no model |

### REPL behaviors

| Behavior | testagent | Notes |
|----------|-----------|-------|
| `!`-shell prefix | `✗ planned` | Spawns a shell for the rest of the line; tracked in [#17](https://github.com/paultyng/testagent/issues/17) |
| `@`-mention / file autocomplete | `not relevant` | TUI-internal |
| `Ctrl+C` | ✓ supported | Quits immediately |
| `Ctrl+D` / EOF | partial | Exits scanner loop (non-interactive); not handled in TUI |
| `Ctrl+G` / open in editor | `not relevant` | TUI-internal |
| `Ctrl+R` reverse search | `not relevant` | TUI-internal |
| `Esc` (cancel in-flight turn) | ✓ supported | Fires `Stop` with `stop_hook_active=true`; `[cancelled]` rendered |
| `Esc Esc` (rewind/checkpoint) | `not relevant` | TUI-internal checkpointing |
| Concurrent input during thinking | ✓ supported | bubbletea TUI accepts input while spinner runs; lines queue |
| Scrollback cap | ✓ supported | `--history-cap N` (default 1000; 0 = unlimited) |
| Vim editor mode | `not relevant` | TUI-internal |

### Hook events

| Event | testagent | Notes |
|-------|-----------|-------|
| `SessionStart` | ✓ supported | Fired at boot (`source=startup`) or resume (`source=resume`); `/restart` fires with `source=<reason>` |
| `SessionEnd` | ✓ supported | Fired on exit; `/restart` fires before the next `SessionStart` |
| `UserPromptSubmit` | ✓ supported | Fired per user input line and `/think` |
| `PostToolUse` | ✓ supported | Fired when `/fake-tool-result` completes a `/fake-tool` block |
| `Stop` | ✓ supported | Fired after each assistant response; `stop_hook_active=true` on `Esc` cancel |
| `PreCompact` | `✗ planned` | Tracked in [#12](https://github.com/paultyng/testagent/issues/12) |
| `PostCompact` | `✗ planned` | Tracked in [#12](https://github.com/paultyng/testagent/issues/12) |
| `Setup` | `✗ planned` | |

---

## Codex

**Upstream version researched:** codex-cli v0.130.0 (2026-05-08) — tag `rust-v0.130.0`
**Local binary version:** `codex --version` → codex-cli 0.130.0

### Flags

Alphabetical by long name. Short flags shown inline. These are global flags for interactive mode; subcommand-specific flags (e.g., `codex exec --ephemeral`) are not modeled in the stub.

| Flag | testagent | Notes |
|------|-----------|-------|
| `--add-dir` | `✗ planned` | Additional writable directories; tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `--ask-for-approval` / `-a` | `not relevant` | Approval policy; no execution engine |
| `--cd` / `-C` | `✗ planned` | Working root override; tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `--config` / `-c` | `not relevant` | Runtime `config.toml` key override; no config system in stub |
| `--dangerously-bypass-approvals-and-sandbox` | `not relevant` | No sandbox in testagent |
| `--disable` | `not relevant` | Feature flag disable; no feature system |
| `--enable` | `not relevant` | Feature flag enable; no feature system |
| `--image` / `-i` | `not relevant` | Image attachment; no model |
| `--local-provider` | `not relevant` | OSS provider (lmstudio/ollama) selection; no model |
| `--model` / `-m` | `accepted` | Parsed by stub (`cmd/codex/codex.go`); silently ignored |
| `--no-alt-screen` | `not relevant` | TUI-internal; alternate screen toggle |
| `--oss` | `not relevant` | Use open-source provider; no model |
| `--profile` / `-p` | `not relevant` | Config profile selection; no config system |
| `--remote` | `not relevant` | Remote app-server websocket endpoint; not applicable |
| `--remote-auth-token-env` | `not relevant` | Remote auth bearer token env var; not applicable |
| `--sandbox` / `-s` | `not relevant` | Sandbox policy (`read-only`, `workspace-write`, `danger-full-access`); no execution engine |
| `--search` | `not relevant` | Enable web search tool; no model |
| `--session` | `accepted` | Testagent-invented stub flag; no real codex equivalent (real resume is `codex resume [SESSION_ID]` subcommand); tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `--version` / `-V` | `not relevant` | testagent uses its own `--version` |

### Slash commands

> **Naming collision:** Codex's `/mcp` lists configured MCP tools (use `/mcp verbose` for details). testagent uses `/mcp-call` for tool dispatch to avoid colliding with this. Codex's `/exit` and `/quit` both exit the CLI; testagent's `/exit [code]` maps to either. Codex's `/clear` clears the terminal and starts a new chat; testagent's `/restart clear` simulates the hook side-effect only. Codex's `/status` shows session config and token usage; testagent has no equivalent.

#### Built-in

All rows from the `SlashCommand` enum in `codex-rs/tui/src/slash_command.rs`. Alphabetical. All `✗ planned` rows tracked in [#13](https://github.com/paultyng/testagent/issues/13) unless noted.

| Command | testagent | Notes |
|---------|-----------|-------|
| `/agent` | `✗ planned` | Switch active agent thread |
| `/apps` | `not relevant` | App/connector management; no connector system |
| `/approve` | `not relevant` | Approve one auto-review denial retry; no approval system |
| `/clear` | `✗ planned` | Clears terminal + starts new chat; `/restart clear` fires hook side-effect only |
| `/collab` | `not relevant` | Collaboration mode (experimental); requires model |
| `/compact` | `✗ planned` | Context summarization; tracked in [#12](https://github.com/paultyng/testagent/issues/12) |
| `/copy` | `not relevant` | Copy last response to clipboard; TUI-internal |
| `/debug-config` | `not relevant` | Config layer debug view; no config system |
| `/diff` | `not relevant` | Show git diff including untracked; TUI-internal |
| `/exit` | `✗ planned` | Exit Codex (also `/quit`); tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `/experimental` | `not relevant` | Toggle experimental features; no feature system |
| `/feedback` | `not relevant` | Send logs to maintainers; TUI-internal |
| `/fork` | `✗ planned` | Fork current chat session |
| `/goal` | `not relevant` | Set/view long-running task goal; requires model |
| `/hooks` | `not relevant` | View/manage lifecycle hooks via TUI; no TUI hook browser |
| `/ide` | `not relevant` | Include IDE selection/open-files context; no IDE integration |
| `/init` | `not relevant` | Create `AGENTS.md` for the project; requires model |
| `/keymap` | `not relevant` | Remap TUI shortcuts; TUI-internal |
| `/logout` | `not relevant` | Log out of Codex; no auth system |
| `/mcp` | `not relevant` | List configured MCP tools; testagent uses `/mcp-call` instead |
| `/memories` | `not relevant` | Configure memory use and generation; requires model |
| `/mention` | `not relevant` | File mention/autocomplete; TUI-internal |
| `/model` | `not relevant` | Model and reasoning effort picker; no model |
| `/new` | `✗ planned` | Start new chat without clearing terminal |
| `/permissions` | `not relevant` | Permission management; no permission system |
| `/personality` | `not relevant` | Communication style picker; requires model |
| `/plan` | `not relevant` | Switch to plan mode; requires model |
| `/plugins` | `not relevant` | Browse plugin marketplace; no plugin system |
| `/ps` | `not relevant` | List background terminals; TUI-internal |
| `/quit` | `✗ planned` | Exit Codex (alias of `/exit`) |
| `/raw` | `not relevant` | Toggle raw scrollback mode for copy-friendly selection; TUI-internal |
| `/realtime` | `not relevant` | Toggle realtime voice mode (experimental) |
| `/rename` | `✗ planned` | Rename the current thread |
| `/resume` | `✗ planned` | Resume a saved chat session |
| `/review` | `not relevant` | Code review of current changes; requires model |
| `/sandbox-add-read-dir` | `not relevant` | Grant sandbox read access to a directory; no sandbox |
| `/settings` | `not relevant` | Configure realtime microphone/speaker; TUI-internal |
| `/setup-default-sandbox` | `not relevant` | Set up elevated agent sandbox; no sandbox |
| `/side` | `not relevant` | Start ephemeral side conversation; requires model |
| `/skills` | `not relevant` | Browse/install user skills; requires model |
| `/status` | `not relevant` | Show session config and token usage; no session state tracking |
| `/statusline` | `not relevant` | Configure status line items; TUI-internal |
| `/stop` | `not relevant` | Stop all background terminals; TUI-internal |
| `/subagents` | `not relevant` | Switch active agent thread (alias of `/agent`) |
| `/theme` | `not relevant` | Syntax highlighting theme picker; TUI-internal |
| `/title` | `not relevant` | Configure terminal title items; TUI-internal |
| `/vim` | `not relevant` | Toggle Vim composer mode; TUI-internal |

#### Plugin/MCP-contributed (service-tier)

| Pattern | testagent | Notes |
|---------|-----------|-------|
| Service-tier commands | `not relevant` | Dynamically contributed by connected MCP servers; no model |

### REPL behaviors

| Behavior | testagent | Notes |
|----------|-----------|-------|
| `!`-shell prefix | `not relevant` | Spawns shell command in the composer; TUI-internal |
| `Ctrl+C` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `Ctrl+D` / EOF | `not relevant` | TUI-internal |
| `Esc` (cancel in-flight turn) | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| External editor (`open_external_editor` action) | `not relevant` | Opens draft in external editor; TUI-internal |
| Transcript overlay (`open_transcript` action) | `not relevant` | TUI-internal |
| Vim composer mode (`/vim` or `toggle_vim_mode`) | `not relevant` | TUI-internal |
| Alternate screen mode / `--no-alt-screen` | `not relevant` | TUI-internal; auto-disables in Zellij |

### Hook events

Hooks are configured in `~/.codex/config.toml` under `[hooks]`. Each event takes an array of `MatcherGroup` objects; each group specifies a `command` (shell string) with optional `async`, `timeout`, and `statusMessage` fields. Note: hook handler shape differs from Claude Code (shell command vs HTTP POST).

| Event | testagent | Notes |
|-------|-----------|-------|
| `SessionStart` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `UserPromptSubmit` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `PreToolUse` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13); no Claude Code equivalent |
| `PostToolUse` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `Stop` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `PreCompact` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `PostCompact` | `✗ planned` | Tracked in [#13](https://github.com/paultyng/testagent/issues/13) |
| `PermissionRequest` | `not relevant` | Approval/permission hook; no permission system in testagent |

### Config and conventions

| Feature | testagent | Notes |
|---------|-----------|-------|
| `~/.codex/config.toml` | `not relevant` | Codex config home (`$CODEX_HOME` overrides); testagent has no Codex config loading |
| `AGENTS.md` project instructions | `not relevant` | Read by the model at session start; testagent has no model |
| `[mcp_servers]` in config.toml | `not relevant` | MCP servers configured via TOML; differs from Claude's `--mcp-config` JSON file |
| `codex mcp add/remove/list` | `not relevant` | Subcommands managing `[mcp_servers]`; no config management in stub |
