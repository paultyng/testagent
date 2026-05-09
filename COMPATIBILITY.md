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
| `--verbose` / `-v` *(global)* | ✓ supported | Logs hook POSTs to stderr |
| `--worktree` / `-w` | `not relevant` | No git worktree management |

### Slash commands

> **Naming collision:** Claude Code's `/mcp` opens a server-management UI. testagent uses `/mcp-call` for tool dispatch to avoid colliding with this. Claude Code's `/exit` exits the CLI; testagent's `/exit [code]` does the same. Claude Code's `/help` shows help; testagent's `/help` does the same.

#### Built-in

| Command | testagent | Notes |
|---------|-----------|-------|
| `/add-dir <path>` | `✗ planned` | |
| `/clear` | `✗ planned` | `/restart clear` simulates the hook side-effect |
| `/compact` | `✗ planned` | `/restart compact` simulates the hook side-effect |
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
| `!`-shell prefix | `not relevant` | TUI-internal; runs shell commands |
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
| `Setup` | `✗ planned` | |
