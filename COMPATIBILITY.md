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

## Platform support

testagent's CI matrix runs on linux, macOS, and Windows on both amd64 and arm64 — release binaries are published for all six combinations on every tagged release.

**Cross-platform (works everywhere):**
- Interactive TUI and `--print` mode rendering
- All slash commands and MCP dispatch
- Claude HTTP and command (shell) hooks
- Flag parsing, settings/config loading, session-id handling

**Unix-only at runtime (Windows is degraded):**
- Shell-runner hooks (Codex `command`-type hooks and Claude `Type="command"` hooks) invoke a POSIX-style shell. On Unix that's `$SHELL -lc <command>`; on Windows the runner routes through `%COMSPEC% /C <command>` so the hook will fire, but the user's command string must be valid `cmd.exe` syntax — typical bash one-liners (`&&`, `||`, `$VAR`, redirection nuance) will NOT work.
- SIGWINCH-driven resize echoes in scanner mode are Unix-only (Windows has no equivalent signal; the resize message is silently disabled).
- The OSC 11 PTY regression test (`pty_e2e_test.go`) is gated to Unix because `creack/pty` isn't usable on Windows. The bug it covers only manifests under PTY anyway.

**Windows caveats:**
- ANSI/styled output requires a recent Windows 10/11 or Windows Server 2022+ runner so VT processing is available. Older Windows configurations may render raw escape codes.
- File-mode permission bits (`os.Chmod 0o600` and friends) are no-ops on Windows. Any future testagent feature that asserts file permissions will need a Unix-only test gate.
- Symlinks require Developer Mode or admin elevation. None of testagent's current paths use them, but future expansion should keep this in mind.

---

## Claude

### Subcommands

| Subcommand | testagent | Notes |
|------------|-----------|-------|
| `claude` (no subcommand) | ✓ supported | Interactive session or `--print` one-shot |
| `claude validate` | ✓ supported (testagent-only) | Validates `--settings` and/or `--mcp-config` without booting a session. Exit `0` clean, `1` validation errors on stderr, `2` usage error. `--strict` adds unknown-field, unknown-event, and unknown-hook-type checks. No real-Claude equivalent — testagent extension for CI use |

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
| `--history-cap` *(global)* | accepted | Parsed; no behavior (TUI uses native terminal scrollback since v0.4; flag retained for back-compat) |
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
| `/clear` | ✓ supported | Fires `SessionEnd(reason=clear)` → `SessionStart(source=clear)`; scrollback pruned to banner + the `/clear` user-echo line |
| `/compact` | ✓ supported | Fires `PreCompact(trigger=manual)` → `SessionEnd(reason=compact)` → `SessionStart(source=compact)` → `PostCompact(trigger=manual)`; scrollback pruned to banner + the `/compact` user-echo line + a `Compacted` marker |
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

The `/fake-*` namespace is reserved for emulation-only commands — slash commands that drive lifecycles the real CLI fires internally (never as a user command). Documented here as a distinct category, not as parity-matrix entries.

| Command | Purpose |
|---------|---------|
| `/fake-auto-compact` | Drives the compact lifecycle with `trigger=auto` (emulates upstream's internal context-fill compaction trigger) |
| `/fake-notification [matcher] [-- message]` | Fires `Notification` (claude-only) with the chosen matcher value; defaults to `permission_prompt` |
| `/fake-permission-request <tool_name> [json-args]` | Fires `PermissionRequest`, waits for the hook's allow/deny decision, renders the outcome |
| `/fake-permission-resolve allow\|deny [reason]` | Resolves an outstanding PreToolUse `ask` state (claude-only); `allow` lets `/fake-tool-result` complete normally, `deny` fires `PostToolUse` with a blocked synthetic response |
| `/fake-tool <name> <json-args>` | Renders a fake tool-use block and fires `PreToolUse`; pair with `/fake-tool-result` to fire `PostToolUse` |
| `/fake-tool-result <json-or-text>` | Completes a pending `/fake-tool`; fires `PostToolUse` hook with the captured input + supplied response + measured duration |
| `/link <url> [text]` | Renders an OSC 8 hyperlink (clickable in supporting terminals); text defaults to URL |
| `/mcp-call <server.tool> <json-args>` | Calls a connected MCP tool; named to avoid `/mcp` collision |
| `/panel <text>` | Renders text in a rounded-border box |
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
| `Shift+Enter` (multi-line input) | ✓ supported | Inserts a newline in the input without submitting; plain `Enter` submits the full multi-line prompt |
| Auto-expanding input height | ✓ supported | Input grows vertically as multi-line content is entered |
| Concurrent input during thinking | ✓ supported | bubbletea TUI accepts input while spinner runs; queued lines render in the bottom pane below the spinner |
| Scrollback | ✓ supported | TUI uses the terminal's native scrollback (PgUp / mouse wheel work without app-side keybindings); `/clear` wipes both visible screen and scrollback via VT escape sequences |
| Vim editor mode | `not relevant` | TUI-internal |

### Hook handler types

Each hook entry in `settings.json` under `hooks.<event>[].hooks[]` carries a `type` discriminator plus type-specific fields. Claude Code's real handler shapes:

| Type | testagent | Notes |
|------|-----------|-------|
| `http` | ✓ supported | POSTs the event JSON body to `url`; `headers` applied; per-hook `timeout` (seconds) honored |
| `command` | ✓ supported | Pipes the event JSON body to `command`'s stdin via `$SHELL -lc` (Unix) / `%COMSPEC% /C` (Windows); per-hook `timeout` honored; stdout/stderr discarded |

Unknown `type` values decode cleanly and are silently skipped at dispatch.

### Matcher patterns

For tool-scoped events (`PreToolUse`, `PostToolUse`, `PermissionRequest`) testagent filters matchers against the active `tool_name`. For `Notification`, the matcher field filters against the documented subtype value (`permission_prompt`, `idle_prompt`, etc.). Other events ignore the matcher field — every registered matcher fires.

**Behavior change (upgrading from ≤ v0.5.0)**: matchers on tool-scoped events were previously fired unconditionally regardless of pattern. Configs that registered `matcher: "Bash"` and relied on the buggy fire-all behavior will now correctly only fire on `Bash` calls. Use `""` or `"*"` for an explicit catch-all.

Pattern grammar (matches Claude Code's documented set):

- `""` or `"*"` — catch-all.
- `"Bash"` — exact-string match (case-sensitive).
- `"Read|Edit|MultiEdit"` — any-of alternation (each segment is an exact literal; only used when no segment contains regex metacharacters).
- anything containing regex metacharacters (`.()[]*+?^$\{}|`) — Go regexp, unanchored substring match. Anchor explicitly via `^…$` for exact matching of patterns that also use regex.

### Hook events

Rows marked `✓ supported` are fired at runtime by testagent. Rows marked `accepted` are in the `claude validate --strict` allowlist (`cmd/claude/validate.go: knownClaudeEvents`) — testagent accepts the config keys without error but does not fire those events. The full set mirrors [code.claude.com/docs/en/hooks](https://code.claude.com/docs/en/hooks).

| Event | testagent | Notes |
|-------|-----------|-------|
| `ConfigChange` | accepted | Documented Claude event; testagent does not fire it |
| `CwdChanged` | accepted | Documented Claude event; testagent does not fire it |
| `Elicitation` | accepted | Documented Claude event; testagent does not fire it |
| `ElicitationResult` | accepted | Documented Claude event; testagent does not fire it |
| `FileChanged` | accepted | Documented Claude event; testagent does not fire it |
| `InstructionsLoaded` | accepted | Documented Claude event; testagent does not fire it |
| `Notification` | ✓ supported | Fired by `/fake-notification [matcher] [-- message]`; matcher defaults to `permission_prompt` |
| `PermissionDenied` | accepted | Documented Claude event; testagent does not fire it |
| `PermissionRequest` | ✓ supported | Fired by `/fake-permission-request <tool_name> [json-args]`; nested `decision.behavior` response shape; 120s default hold-open; renders the aggregated decision |
| `PostCompact` | ✓ supported | Fired after the SessionEnd → SessionStart pair for `/compact` and `/fake-auto-compact`; trigger matches the PreCompact value |
| `PostToolBatch` | accepted | Documented Claude event; testagent does not fire it |
| `PostToolUse` | ✓ supported | Fired when `/fake-tool-result` completes a `/fake-tool` block |
| `PostToolUseFailure` | accepted | Documented Claude event; testagent does not fire it |
| `PreCompact` | ✓ supported | Fired before the SessionEnd → SessionStart pair for `/compact` (`trigger=manual`) and `/fake-auto-compact` (`trigger=auto`) |
| `PreToolUse` | ✓ supported | Fired when `/fake-tool` opens a tool-use block (before `/fake-tool-result`); body carries `tool_input` but no `tool_response` or `duration_ms` |
| `SessionEnd` | ✓ supported | Fired on exit; `/clear` and `/compact` fire before the next `SessionStart` |
| `SessionStart` | ✓ supported | Fired at boot (`source=startup`) or resume (`source=resume`); `/clear` and `/compact` fire with `source=clear` or `source=compact` |
| `Setup` | accepted | Documented Claude event; testagent does not fire it |
| `Stop` | ✓ supported | Fired after each assistant response; `stop_hook_active=true` on `Esc` cancel |
| `StopFailure` | accepted | Documented Claude event; testagent does not fire it |
| `SubagentStart` | accepted | Documented Claude event; testagent does not fire it |
| `SubagentStop` | accepted | Documented Claude event; testagent does not fire it |
| `TaskCompleted` | accepted | Documented Claude event; testagent does not fire it |
| `TaskCreated` | accepted | Documented Claude event; testagent does not fire it |
| `TeammateIdle` | accepted | Documented Claude event; testagent does not fire it |
| `UserPromptExpansion` | accepted | Documented Claude event; testagent does not fire it |
| `UserPromptSubmit` | ✓ supported | Fired per user input line and `/think` |
| `WorktreeCreate` | accepted | Documented Claude event; testagent does not fire it |
| `WorktreeRemove` | accepted | Documented Claude event; testagent does not fire it |

---

## Codex

**Upstream version researched:** codex-cli v0.130.0 (2026-05-08) — tag `rust-v0.130.0`
**Local binary version:** `codex --version` → codex-cli 0.130.0

### Subcommands

Codex's user-facing surface is divided across multiple subcommands; orchestrators typically invoke a specific one (`codex exec`, `codex resume`, etc.) rather than the bare interactive form. This section tracks subcommand coverage first, then global flags (used in interactive mode), then per-subcommand flag tables for the subcommands with non-trivial flag surfaces.

| Subcommand | testagent | Notes |
|------------|-----------|-------|
| `codex` (no subcommand) | ✓ supported | Interactive session via the shared engine |
| `codex resume <SESSION_ID>` | ✓ supported | Boots interactive with `Resumed=true` (codex's analog of claude `--resume`) |
| `codex exec <prompt>` | ✓ supported | `text` / `json` / `stream-json` output formats; see [#### codex exec](#codex-exec) below for the flag surface |
| `codex fork` | `✗ planned` | Fork current chat session; tracked in [#34](https://github.com/paultyng/testagent/issues/34) |
| `codex review` | `✗ planned` | Code review of changes; requires model — out of scope unless a fake review-output mode is added |
| `codex login` | `✗ planned` | Tracked in [#35](https://github.com/paultyng/testagent/issues/35) |
| `codex logout` | `✗ planned` | Tracked in [#35](https://github.com/paultyng/testagent/issues/35) |
| `codex mcp add/list/remove` | `✗ planned` | Tracked in [#37](https://github.com/paultyng/testagent/issues/37) |
| `codex validate` | ✓ supported (testagent-only) | Validates `$CODEX_HOME/config.toml` (or `~/.codex/config.toml`) without booting a session. Exit `0` clean, `1` validation errors on stderr, `2` usage error. `--strict` adds unknown-key, unknown-event, and unknown-hook-type checks. No real-codex equivalent — testagent extension for CI use |

#### codex exec

`codex exec <prompt>` is codex's non-interactive one-shot — the analog of `claude --print`. Lifecycle: `session_start` → `user_prompt_submit` → emit per `--output-format` → `stop`. Codex has no `session_end` event.

| Flag | testagent | Notes |
|------|-----------|-------|
| `--output-format text\|json\|stream-json` | ✓ supported | `stream-json` emits upstream codex-rs/exec's `ThreadEvent` JSONL sequence (`thread.started` → `turn.started` → `item.started`/`item.completed` for the `agent_message` item → `turn.completed`). `json` emits a single summary object (`type: turn.completed`, `thread_id`, `final_message`, `usage`) — testagent-specific convenience; upstream codex has no equivalent single-shot JSON mode. Fields that require a real model (`reasoning_output_tokens`, `cached_input_tokens`, tool / file-change items) are zero or absent per the no-fabrication rule. Upstream codex's canonical flag is `--json` (alias `--experimental-json`); testagent uses `--output-format` to mirror the claude side. |
| `--ephemeral` | `✗ planned` | Run without persisting the session; tracked in [#32](https://github.com/paultyng/testagent/issues/32) |

### Flags (global / interactive)

Alphabetical by long name. Short flags shown inline. **These are global flags for the bare interactive `testagent codex` invocation.** Subcommand-specific flags (e.g., `codex exec --ephemeral`) live under the relevant subcommand's sub-section above, not here.

| Flag | testagent | Notes |
|------|-----------|-------|
| `--add-dir` | ✓ supported | Repeatable; stored, count surfaced in status line |
| `--ask-for-approval` / `-a` | accepted | Parsed; surfaced in status line; semantics tracked in [#38](https://github.com/paultyng/testagent/issues/38) |
| `--cd` / `-C` | ✓ supported | Honored via `os.Chdir` before any cwd-relative work |
| `--config` / `-c` | accepted | Repeatable `KEY=VALUE`; parsed and surfaced in status line; no value-application semantics |
| `--dangerously-bypass-approvals-and-sandbox` | accepted | Parsed; no behavior (no sandbox in testagent) |
| `--disable` | accepted | Repeatable; parsed; no behavior (no feature system) |
| `--enable` | accepted | Repeatable; parsed; no behavior (no feature system) |
| `--image` / `-i` | accepted | Repeatable; parsed; no behavior (no model) |
| `--local-provider` | accepted | Parsed; no behavior (no model) |
| `--model` / `-m` | accepted | Parsed; surfaced in status line; not modeled |
| `--no-alt-screen` | accepted | Parsed; no behavior (testagent always uses alt screen) |
| `--oss` | accepted | Parsed; no behavior (no model) |
| `--profile` / `-p` | accepted | Parsed; no behavior (no config-profile system) |
| `--remote` | `not relevant` | Remote app-server websocket endpoint; not applicable |
| `--remote-auth-token-env` | `not relevant` | Remote auth bearer token env var; not applicable |
| `--sandbox` / `-s` | accepted | Parsed; surfaced in status line; semantics tracked in [#38](https://github.com/paultyng/testagent/issues/38) |
| `--search` | accepted | Parsed; no behavior (no model) |
| `--version` / `-V` | `not relevant` | testagent uses its own `--version` |

### Slash commands

> **Naming collision:** Codex's `/mcp` lists configured MCP tools (use `/mcp verbose` for details). testagent uses `/mcp-call` for tool dispatch to avoid colliding with this. Codex's `/exit` and `/quit` both exit the CLI; testagent's `/exit [code]` maps to either. testagent's `/clear` wipes the visible screen and scrollback (via VT escape codes) and fires the hook side-effect. Codex's `/status` shows session config and token usage; testagent has no equivalent.

#### Built-in

Visible release commands from the `SlashCommand` enum in `codex-rs/tui/src/slash_command.rs`. Alphabetical. Debug-only / hidden commands (currently `/rollout` and `/test-approval`) are intentionally omitted — they aren't user-facing in a normal codex install. All `✗ planned` rows tracked in [#13](https://github.com/paultyng/testagent/issues/13) unless noted.

| Command | testagent | Notes |
|---------|-----------|-------|
| `/agent` | `✗ planned` | Switch active agent thread |
| `/apps` | `not relevant` | App/connector management; no connector system |
| `/approve` | `not relevant` | Approve one auto-review denial retry; no approval system |
| `/clear` | ✓ supported | Fires `session_end(reason=clear)` → `session_start(source=clear)`; scrollback pruned to banner + the `/clear` user-echo line. Real codex's post-`/clear` rendering not yet verified — mirrors claude shape for now |
| `/collab` | `not relevant` | Collaboration mode (experimental); requires model |
| `/compact` | ✓ supported | Fires `pre_compact(trigger=manual)` → `session_end(reason=compact)` → `session_start(source=compact)` → `post_compact(trigger=manual)`; scrollback pruned to banner + the `/compact` user-echo line + a `Compacted` marker. Real codex's post-`/compact` rendering not yet verified — mirrors claude shape for now |
| `/copy` | `not relevant` | Copy last response to clipboard; TUI-internal |
| `/debug-config` | `not relevant` | Config layer debug view; no config system |
| `/diff` | `not relevant` | Show git diff including untracked; TUI-internal |
| `/exit` | ✓ supported | Accepts optional exit code (alias `/quit`) |
| `/experimental` | `not relevant` | Toggle experimental features; no feature system |
| `/fast` | `not relevant` | Toggle "fast" reasoning-effort tier; requires model |
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
| `/quit` | ✓ supported | Alias of `/exit` |
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

Hooks are configured in `~/.codex/config.toml` under `[hooks]`. Each event takes an array of `MatcherGroup` objects: `{matcher: string, hooks: []Hook}`. Each `Hook` carries a `type` discriminator — `command`, `prompt`, or `agent` — plus type-specific fields (`command`, `timeout`, `async` for the command type). testagent currently fires the `command` type only; `prompt` and `agent` entries decode cleanly but are silently skipped at dispatch time. Hook handler shape differs from Claude Code (shell command vs HTTP POST). Commands run via `$SHELL -lc <cmd>` on Unix and `%COMSPEC% /C <cmd>` on Windows, mirroring upstream codex's `default_shell_command`. The `matcher` field follows the same grammar as the claude side (`""`/`"*"` catch-all, exact, `A|B|C` alternation, otherwise regex) and is consulted for tool-scoped events (`pre_tool_use`, `post_tool_use`, `permission_request`).

| Event | testagent | Notes |
|-------|-----------|-------|
| `SessionStart` | ✓ supported | Fires on session boot or `codex resume`; emits `CODEX_HOOK_SOURCE=startup\|resume` |
| `UserPromptSubmit` | ✓ supported | Fires per user input line; emits `CODEX_HOOK_PROMPT` |
| `PreToolUse` | ✓ supported | Fired when `/fake-tool` opens a tool-use block; emits `CODEX_HOOK_TOOL_NAME`, `CODEX_HOOK_TOOL_INPUT` (JSON), `CODEX_HOOK_TOOL_USE_ID` |
| `PostToolUse` | ✓ supported | Fired when `/fake-tool-result` completes a `/fake-tool` block; emits `CODEX_HOOK_TOOL_RESPONSE` (JSON) and `CODEX_HOOK_DURATION_MS` in addition to the pre fields |
| `Stop` | ✓ supported | Fires after each assistant response; emits `CODEX_HOOK_LAST_ASSISTANT_MESSAGE` |
| `PreCompact` | ✓ supported | Fires before SessionEnd → SessionStart on `/compact` and `/fake-auto-compact`; emits `CODEX_HOOK_TRIGGER=manual\|auto` |
| `PostCompact` | ✓ supported | Fires after SessionEnd → SessionStart on `/compact` and `/fake-auto-compact`; emits `CODEX_HOOK_TRIGGER=manual\|auto` |
| `PermissionRequest` | ✓ supported | Fired by `/fake-permission-request <tool_name> [json-args]`; nested `decision.behavior` response from script stdout (exit 0) or stderr (exit 2); 120s default hold-open via per-matcher `timeout`; exit 0 with no stdout renders as `permission timed out: deny` |

### Config and conventions

| Feature | testagent | Notes |
|---------|-----------|-------|
| `~/.codex/config.toml` | partial | Loaded if present; `$CODEX_HOME` honored; `[hooks]` table consumed for `session_start`/`user_prompt_submit`/`pre_tool_use`/`post_tool_use`/`stop`/`pre_compact`/`post_compact`/`permission_request`; `[mcp_servers]` parsed but not yet consumed |
| `AGENTS.md` project instructions | partial | Presence surfaced in status line; content not interpreted (testagent has no model) |
| `[mcp_servers]` in config.toml | `✗ planned` | Parsed by config skeleton; not yet consumed by the MCP client (tracked in [#13](https://github.com/paultyng/testagent/issues/13)) |
| `codex mcp add/remove/list` | `not relevant` | Subcommands managing `[mcp_servers]`; no config management in stub |

---

## Cursor

**Upstream version researched:** Cursor CLI 2026.05.09-0afadcc
**Local binary version:** `cursor agent --version` → 2026.05.09-0afadcc

The `testagent cursor` subcommand boots the shared engine REPL with the full upstream global flag surface `accepted` (parsed without error). Subcommands login/logout/status/about/models/update/create-chat/resume/ls are canned-output stubs; `mcp list / list-tools / enable / disable` are wired against `internal/mcp.Client`. Hooks fire via `internal/cursorhooks` (top-level `permission` wire shape); `--print` honors `--output-format text|json|stream-json` per cursor.com/docs/cli/reference/output-format; `.cursor/rules/*.mdc` are walked and surfaced in the banner with activation-mode counts. Remaining work: `internal/mcp` stdio support, typed `tool_call` frames in stream-json. Tracked in [#14](https://github.com/paultyng/testagent/issues/14).

### Subcommands

Cursor's user-facing surface is under `cursor agent`; bare `cursor agent [prompt...]` is the interactive REPL.

| Subcommand | testagent | Notes |
|------------|-----------|-------|
| `cursor agent` (no subcommand) | `partial` | Phase 1 prints a skeleton banner; interactive REPL lands in Phase 2 ([#14](https://github.com/paultyng/testagent/issues/14)) |
| `cursor agent --print` | `✓ supported` | Non-interactive one-shot; `--output-format text\|json\|stream-json` honored; cursor stream-json frame set per cursor.com/docs/cli/reference/output-format |
| `cursor agent about` | `accepted` | Canned `name`+`version`; `--format text\|json` honored |
| `cursor agent create-chat` | `accepted` | Returns a canned chat ID |
| `cursor agent generate-rule` | `not relevant` | Interactive rule-authoring wizard; requires model |
| `cursor agent install-shell-integration` | `not relevant` | Writes to `~/.zshrc`; not in scope for a fake agent |
| `cursor agent login` | `accepted` | Auth no-op stub |
| `cursor agent logout` | `accepted` | Session-clear no-op stub |
| `cursor agent ls` | `accepted` | Canned chat list |
| `cursor agent mcp list` | `✓ supported` | Reads merged `.cursor/mcp.json` + `~/.cursor/mcp.json`; prints name / status / transport |
| `cursor agent mcp list-tools <id>` | `partial` | HTTP servers connect via `internal/mcp.Client`; stdio servers return a not-yet-supported error (Phase 2 follow-up) |
| `cursor agent mcp login <id>` | `✗ planned` | OAuth handshake; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `cursor agent mcp enable <id>` | `✓ supported` | Round-trips `disabled: false` to `~/.cursor/mcp.json` (atomic write) |
| `cursor agent mcp disable <id>` | `✓ supported` | Round-trips `disabled: true` to `~/.cursor/mcp.json` (atomic write) |
| `cursor agent models` | `accepted` | Canned model list, one per line |
| `cursor agent resume` | `accepted` | Canned-output stub; optional positional `<chat-id>` |
| `cursor agent status\|whoami` | `accepted` | Canned auth status; `--format text\|json` honored |
| `cursor agent uninstall-shell-integration` | `not relevant` | Symmetric to install-shell-integration |
| `cursor agent update` | `not relevant` | Self-update; testagent has its own release cadence |

### Flags (global / interactive)

Alphabetical by long name. Short flags shown inline.

| Flag | testagent | Notes |
|------|-----------|-------|
| `--api-key <key>` | `accepted` | Auth key; parsed and discarded |
| `--approve-mcps` | `accepted` | Auto-approve MCP servers (no permission engine) |
| `--continue` | `accepted` | Continue previous session (no session engine) |
| `-f` / `--force` / `--yolo` | `accepted` | Approval bypass; parsed and discarded |
| `-H` / `--header <header>` | `accepted` | Custom HTTP header; parsed and discarded (repeatable) |
| `--list-models` | `✗ planned` | Print model list and exit; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `-m` / `--model <name>` | `accepted` | Model name; parsed and discarded |
| `--mode <plan\|ask>` | `accepted` | Banner change lands in Phase 3 ([#14](https://github.com/paultyng/testagent/issues/14)) |
| `--output-format <text\|json\|stream-json>` | `✓ supported` | Output formatter for `--print`; text default, json single object, stream-json NDJSON (system/user/assistant/result) |
| `-p` / `--print` | `✓ supported` | Non-interactive one-shot; reads positional or stdin prompt, echoes via the selected `--output-format` |
| `--plan` | `✗ planned` | Shorthand for `--mode=plan`; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `--plugin-dir <path>` | `accepted` | Local plugin dir; no plugin engine (repeatable) |
| `--resume [chatId]` | `accepted` | Persistent flag parsed; positional-arg subcommand stub returns canned output |
| `--sandbox <enabled\|disabled>` | `accepted` | Sandbox override; parsed and discarded |
| `--skip-worktree-setup` | `✗ planned` | Skip worktree setup scripts; not yet wired |
| `--stream-partial-output` | `✗ planned` | Token-level deltas in `stream-json`; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `--trust` | `accepted` | Trust workspace without prompting; parsed and discarded |
| `-v` / `--version` | `not relevant` | testagent uses its own `--version` |
| `--workspace <path>` | `✓ supported` | Chdirs before the skeleton banner prints (mirrors codex `--cd`) |
| `-w` / `--worktree [name]` | `accepted` | Worktree creation flag; parsed and discarded |
| `--worktree-base <branch>` | `accepted` | Worktree base ref; parsed and discarded |

### Slash commands

> **Naming collision:** Cursor's `/mcp list` opens an interactive MCP browser; testagent uses `/mcp-call` for tool dispatch to avoid the collision. Cursor has no top-level `/exit` slash — Ctrl+D (double-press) exits; Ctrl+C also exits. Source: [cursor.com/docs/cli/using](https://cursor.com/docs/cli/using).

#### Built-in

Alphabetical.

| Command | testagent | Notes |
|---------|-----------|-------|
| `/about` | `accepted` | Stub: prints testagent + cursor adapter identity |
| `/ask` | `accepted` | Stub: prints "entering ask mode"; real banner-state toggle is engine work (tracked in [#14](https://github.com/paultyng/testagent/issues/14)) |
| `/compress` | `✓ supported` | Alias for `/compact` — fires PreCompact → SessionEnd → SessionStart → PostCompact (trigger=manual) |
| `/mcp` / `/mcp list` | `accepted` | Stub: prints connected MCP server names + tool counts (real upstream is an interactive browser) |
| `/model` | `accepted` | Stub: prints "model: testagent-stub" |
| `/plan` | `accepted` | Stub: prints "entering plan mode"; real banner-state toggle is engine work (tracked in [#14](https://github.com/paultyng/testagent/issues/14)) |
| `/resume` | `accepted` | Stub: prints "no prior session" (real REPL resume is `cursor resume <id>` subcommand) |
| `/setup-terminal` | `accepted` | Stub: prints "terminal integration: already configured" |
| `/usage` | `accepted` | Stub: prints zeros (testagent never calls an LLM) |

#### Plugin-contributed (skills)

| Pattern | testagent | Notes |
|---------|-----------|-------|
| Plugin-contributed slashes | `not relevant` | Dynamic; depend on installed plugin set; no plugin system |

### REPL behaviors

| Behavior | testagent | Notes |
|----------|-----------|-------|
| `Ctrl+C` / `Ctrl+D` (exit) | `✗ planned` | Cursor has no `/exit` slash; Ctrl+D (double-press) is documented exit; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| Inline `--print` deltas (`--stream-partial-output`) | `✗ planned` | NDJSON text-deltas in stream-json; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| Alternate screen / TUI mode | `not relevant` | TUI-internal |
| Vim composer mode | `not relevant` | TUI-internal |

### Hook events

Hooks are configured in `.cursor/hooks.json` with a 4-level priority cascade (enterprise > team > project > user). testagent's MVP will cover project-level only; other tiers are out of scope. Response wire is **top-level** `{"permission": "allow"|"deny"|"ask", "user_message": "...", "agent_message": "..."}` — distinct from claude/codex's nested shape. Source: [cursor.com/docs/hooks](https://cursor.com/docs/hooks).

| Event | testagent | Notes |
|-------|-----------|-------|
| `afterAgentResponse` | `✗ planned` | Fires after each assistant message completes; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `afterAgentThought` | `not relevant` | Tied to model thinking internals |
| `afterFileEdit` | `✗ planned` | After a file edit lands; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `afterMCPExecution` | `✗ planned` | After an MCP tool call; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `afterShellExecution` | `✗ planned` | After a shell command runs; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `afterTabFileEdit` | `not relevant` | IDE-tab inline-completion event; no IDE integration |
| `beforeMCPExecution` | `✗ planned` | Before an MCP tool call; can allow/deny; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `beforeReadFile` | `✗ planned` | Before a file read; can block; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `beforeShellExecution` | `✗ planned` | Before a shell command; can allow/deny; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `beforeSubmitPrompt` | `✗ planned` | Before model receives user prompt; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `beforeTabFileRead` | `not relevant` | IDE-tab inline-completion event; no IDE integration |
| `postToolUse` | `✗ planned` | After a tool call succeeds; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `postToolUseFailure` | `✗ planned` | After a tool call fails or is denied; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `preCompact` | `✗ planned` | Before context compaction; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `preToolUse` | `✗ planned` | Before any tool call; can allow/deny/ask; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `sessionEnd` | `✗ planned` | Session terminates; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `sessionStart` | `✗ planned` | Session begins; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `stop` | `✗ planned` | Agent loop ends (analog of claude's `Stop`); tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `subagentStart` | `✗ planned` | Before spawning a Task tool subagent; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `subagentStop` | `✗ planned` | After a Task tool subagent completes; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `workspaceOpen` | `not relevant` | App-lifecycle event; fires on workspace folder change; no app shell |

### Config and conventions

| Feature | testagent | Notes |
|---------|-----------|-------|
| `.cursor/hooks.json` (project) | `✗ planned` | Project-scoped hook config; MVP covers this level only; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `~/.cursor/hooks.json` (user) | `not relevant` | User-scoped tier of the priority cascade; out of scope for MVP |
| Team-scoped hooks | `not relevant` | Distributed via Cursor dashboard; no central server |
| Enterprise-scoped hooks | `not relevant` | `/Library/Application Support/Cursor/hooks.json`; out of scope |
| `~/.cursor/mcp.json` (global) | `✗ planned` | Global MCP server config; identical shape to claude's `--mcp-config`; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `.cursor/mcp.json` (project) | `✗ planned` | Project-level override; project config wins; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `cursor agent mcp` subcommands | `✗ planned` | list/list-tools/login/enable/disable surface; tracked in [#14](https://github.com/paultyng/testagent/issues/14) |
| `~/.cursor/cli-config.json` | `✗ planned` | `approvalMode`, sandbox, permissions; will be `accepted` (parsed, not enforced) |
| `approvalMode` token grammar | `not relevant` | Allowlist tokens (`Shell(...)`, `Read(...)`, `Mcp(server:tool)`); no permission engine |
| `sandbox.json` | `not relevant` | Sandbox mode picker; no sandbox; source: [cursor.com/docs/reference/sandbox](https://cursor.com/docs/reference/sandbox) |
| `AGENTS.md` project instructions | `partial` | Same file codex reads; size surfaced in status line; content not interpreted (testagent has no model) |
| `.cursor/rules/*.mdc` | `partial` | Walks `.cursor/rules/*.mdc` and surfaces a `rules: N (a always, g glob, i intelligent, m manual)` line in the banner. Rule body content is not interpreted (testagent has no model) — orchestrator-parity surfacing only. |
| `.cursorrules` (legacy) | `not relevant` | Deprecated; not loaded by current agent CLI |
| Plugins (`~/.cursor/plugins/local/<name>`) | `not relevant` | Dynamic; depend on installed plugin set |
| `--plugin-dir <path>` | `accepted` | Local plugin discovery flag; parsed and discarded (no plugin engine) |
| Worktree integration (`--worktree` / `--worktree-base`) | `accepted` | Both flags parsed and discarded; no worktree management in stub |
