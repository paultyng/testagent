# testagent

![testagent in action](demo/hero.gif)

A fake CLI agent for testing orchestration tooling that wraps real coding agents (Claude Code, Codex, Gemini, GitHub Copilot CLI, etc.). It runs as an interactive PTY process and emits the same kinds of terminal artifacts and protocol traffic as a real agent — without calling any LLM.

The point isn't "no LLM" as a virtue — wiring a local LLM into your test harness is a perfectly valid choice for some workflows. testagent is explicitly *not* that. Its value is **deterministic, scripted output**: the same `/fake-tool`, `/fake-tool-result`, hook fires, and MCP traffic on every run, so test assertions stay stable.

Use it when you're building something that *drives* a coding agent (a TUI wrapper, a session manager, a multi-agent orchestrator) and you need a deterministic, network-free fake to exercise the integration.

## Install

```sh
go install github.com/paultyng/testagent@latest
```

## What it does

The argv shape is `testagent [global-flags] <subcommand> [subcommand-flags] [positional]`. Bare invocation defaults to the `claude` subcommand, so existing scripts that pre-date the split keep working:

```sh
testagent --session-id sid-x --settings ./s.json   # defaults to: testagent claude ...
testagent claude --session-id sid-x --settings ./s.json   # same thing, explicit
```

**Global flags** (engine-level — same across vendors):

- `--history-cap N` — interactive scrollback cap (default 1000; 0 = unlimited)
- `--verbose` / `-v` — log every hook POST to stderr (`hook <event> POST <url> <status> <elapsed> <bodysize> [err=...]`)
- `--auto-exit DUR`, `--exit-after N`, `--delay DUR` — pacing knobs for headless tests

**Claude subcommand flags** (argv-compatible with Claude Code):

- `--session-id <uuid>` / `--resume <uuid>` — session identity
- `--settings <path>` — Claude-shaped settings JSON; URLs receive HTTP hook POSTs
- `--mcp-config <path>` — MCP server config JSON; testagent connects, handshakes, and dispatches `tools/call`
- `--append-system-prompt <text>` — accepted, displayed in the loaded-status line
- `--add-dir <path>` — repeatable
- `--print` / `-p` — non-interactive one-shot
- `--output-format text|json|stream-json` — output shape for `--print`
- `-n` / `--name` — banner label

**Codex subcommand** is a stub — accepts `--session` and `--model` and prints "not yet implemented." Real codex behavior tracked at [#13](https://github.com/paultyng/testagent/issues/13).

In interactive mode, lines starting with `/` are slash commands that synthesize specific UI primitives. Type `/help` for the list:

- `/exit [code]`
- `/fake-tool <name> <json-args>`
- `/fake-tool-result <json-or-text>`
- `/mcp-call <server.tool> <json-args>` — named to avoid colliding with real Claude Code's `/mcp` (server-management UI)
- `/panel <text>`
- `/restart [clear|compact]` — fires `SessionEnd` then `SessionStart` without leaving the process; pass `clear` (default) or `compact` to choose the matcher value Claude would emit on `/clear` vs `/compact`
- `/stream <text>`
- `/think [<duration>] <text>`

Anything else is echoed back, just like the original PTY-echo behavior.

## Interactive mode

When stdin is a TTY, testagent runs a [bubbletea](https://github.com/charmbracelet/bubbletea) TUI in the alternate screen:

- Keystrokes are accepted concurrently with the thinking spinner. You can type the next prompt (or a slash command) while the agent is "thinking" — submitted lines queue and run in order once the current turn completes.
- Press **Esc** to cancel the in-flight thinking turn. A `[cancelled]` line is rendered, the `Stop` hook fires with `stop_hook_active=true` and an empty `last_assistant_message`, and queued prompts continue (use Esc, then `/exit`, to bail out mid-turn).
- **Ctrl+C** quits immediately.
- Scrollback is capped at `--history-cap` lines (default 1000; oldest dropped first).

When stdin is **not** a TTY (piped input, `--print`), testagent falls back to a line-scanner loop with inline rendering — that's the path the e2e tests exercise.

## Hooks

When `--settings` declares hook URLs, testagent POSTs Claude-Code-shaped event bodies on the appropriate lifecycle moments: `SessionStart` at boot (`source=startup`, or `source=resume` when `--resume` is set), `UserPromptSubmit` per user input (raw input AND `/think`), `Stop` after each assistant response, `PostToolUse` when a `/fake-tool` block is closed by `/fake-tool-result` (with the captured `tool_input`, the supplied `tool_response`, and measured `duration_ms`), and `SessionEnd` on shutdown. `/restart [reason]` fires `SessionEnd` then `SessionStart` back-to-back with the same matcher value (`clear` or `compact`), simulating a Claude `/clear` or `/compact` reset on the wire.

## MCP

When `--mcp-config` lists servers, testagent (via `mark3labs/mcp-go`) performs the standard handshake (`initialize` → `notifications/initialized` → `tools/list`) at startup, exposes the tool list to the slash-command grammar, and dispatches `tools/call` when `/mcp-call <server>.<tool>` is invoked.

## Non-interactive (`--print`)

```sh
testagent claude --print --output-format stream-json "summarize the diff"
# or, equivalently:
testagent --print --output-format stream-json "summarize the diff"
```

Emits `{"type":"system","subtype":"init",...}` then `{"type":"assistant",...}` then `{"type":"result",...}` — three frames matching Claude Code's stream-json shape.

## License

MIT.
