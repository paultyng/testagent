# COMPATIBILITY.md — skeleton

Copy this into `COMPATIBILITY.md` at the repo root and fill in each row.

---

<!-- BEGIN TEMPLATE -->
# Compatibility

testagent emulates real CLI agents. This matrix tracks which upstream features are implemented, accepted (parsed but no-op), or intentionally absent.

**Upstream version researched:** Claude Code vX.Y.Z (YYYY-MM-DD)
**Local binary version:** `claude --version` — X.Y.Z

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

Alphabetical by long name. Short flags shown inline in the first column.

| Flag | testagent | Notes |
|------|-----------|-------|
| `--add-dir` | ✓ supported | Repeatable; stored, shown in status line |
| `--append-system-prompt` | accepted | Displayed in loaded-status line; not used |
| `--continue` / `-c` | `✗ planned` | |
| `--mcp-config` | ✓ supported | Connects, handshakes, exposes tools |
| `--model` | `not relevant` | No model in testagent |
| `--name` / `-n` | ✓ supported | Shown in banner |
| `--output-format` | ✓ supported | `text`, `json`, `stream-json` with `--print` |
| `--print` / `-p` | ✓ supported | Non-interactive one-shot mode |
| `--resume` / `-r` | ✓ supported | Sets session ID; fires `source=resume` hook |
| `--session-id` | ✓ supported | UUID for the session |
| `--settings` | ✓ supported | Loads hook URLs; Claude-shaped JSON |
| `--verbose` / `-v` | ✓ supported | Logs hook POSTs to stderr |
| `--version` / `-v` | `not relevant` | testagent uses its own `--version` |
| <!-- ADD ROWS --> | | |

### Slash commands

> **Naming collision:** Claude Code's `/mcp` opens a server-management UI. testagent uses `/mcp-call` for tool dispatch to avoid colliding with this. Claude Code's `/exit` exits the CLI; testagent's `/exit [code]` does the same. Claude Code's `/help` shows help; testagent's `/help` does the same.

#### Built-in

| Command | testagent | Notes |
|---------|-----------|-------|
| `/clear` | `✗ planned` | `/restart clear` simulates the hook side-effect |
| `/compact` | `✗ planned` | `/restart compact` simulates the hook side-effect |
| `/exit` | ✓ supported | Accepts optional exit code |
| `/help` | ✓ supported | Lists testagent slash commands |
| `/mcp` | `not relevant` | testagent uses `/mcp-call` instead |
| <!-- ADD ROWS --> | | |

#### Bundled skill

| Command | testagent | Notes |
|---------|-----------|-------|
| `/batch` | `not relevant` | Requires model |
| `/debug` | `not relevant` | Requires model |
| `/simplify` | `not relevant` | Requires model |
| <!-- ADD ROWS --> | | |

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
| `Ctrl+D` | `not relevant` | EOF exits scanner loop (non-interactive only) |
| `Esc` (cancel in-flight turn) | ✓ supported | Fires `Stop` with `stop_hook_active=true` |
| `Esc Esc` (rewind) | `not relevant` | TUI-internal checkpointing |
| Vim editor mode | `not relevant` | TUI-internal |
| Scrollback cap | ✓ supported | `--history-cap N` (default 1000) |
| <!-- ADD ROWS --> | | |
<!-- END TEMPLATE -->
