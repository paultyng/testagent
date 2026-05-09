# testagent

![testagent in action](demo/hero.gif)

A fake CLI agent for testing orchestration tooling that wraps real coding agents (Claude Code, Codex, Gemini, GitHub Copilot CLI, etc.). It runs as an interactive PTY process and emits the same kinds of terminal artifacts and protocol traffic as a real agent — without calling any LLM API.

Use it when you're building something that *drives* a coding agent (a TUI wrapper, a session manager, a multi-agent orchestrator) and you need a deterministic, network-free fake to exercise the integration.

## Install

```sh
go install github.com/paultyng/testagent@latest
```

## What it does

Argv-compatible with Claude Code's flag surface:

- `--session-id <uuid>` / `--resume <uuid>` — session identity
- `--settings <path>` — Claude-shaped settings JSON; URLs receive HTTP hook POSTs
- `--mcp-config <path>` — MCP server config JSON; testagent connects, handshakes, and dispatches `tools/call`
- `--append-system-prompt <text>` — accepted, displayed in the loaded-status line
- `--add-dir <path>` — repeatable
- `--print` / `-p` — non-interactive one-shot
- `--output-format text|json|stream-json` — output shape for `--print`
- `-n` / `--name` — banner label
- `--delay`, `--auto-exit`, `--exit-after` — pacing knobs for headless tests
- `--history-cap N` — interactive scrollback cap (default 1000; 0 = unlimited)
- `--verbose` / `-v` — log every hook POST to stderr (`hook <event> POST <url> <status> <elapsed> <bodysize> [err=...]`)

In interactive mode, lines starting with `/` are slash commands that synthesize specific UI primitives. Type `/help` for the list:

- `/exit [code]`
- `/mcp <server.tool> <json-args>`
- `/md <markdown>`
- `/panel <text>`
- `/result <json-or-text>`
- `/stream <text>`
- `/think <text>`
- `/tool <name> <json-args>`

Anything else is echoed back, just like the original PTY-echo behavior.

## Interactive mode

When stdin is a TTY, testagent runs a [bubbletea](https://github.com/charmbracelet/bubbletea) TUI in the alternate screen:

- Keystrokes are accepted concurrently with the thinking spinner. You can type the next prompt (or a slash command) while the agent is "thinking" — submitted lines queue and run in order once the current turn completes.
- Press **Esc** to cancel the in-flight thinking turn. A `[cancelled]` line is rendered, the `Stop` hook fires with `stop_hook_active=true` and an empty `last_assistant_message`, and queued prompts continue (use Esc, then `/exit`, to bail out mid-turn).
- **Ctrl+C** quits immediately.
- Scrollback is capped at `--history-cap` lines (default 1000; oldest dropped first).

When stdin is **not** a TTY (piped input, `--print`), testagent falls back to a line-scanner loop with inline rendering — that's the path the e2e tests exercise.

## Hooks

When `--settings` declares hook URLs, testagent POSTs Claude-Code-shaped event bodies on the appropriate lifecycle moments: `UserPromptSubmit` per user input, `Stop` after each assistant response, `PostToolUse` when a `/tool` block is closed by `/result` (with the captured `tool_input`, the supplied `tool_response`, and measured `duration_ms`), `SessionEnd` on shutdown.

## MCP

When `--mcp-config` lists servers, testagent (via `mark3labs/mcp-go`) performs the standard handshake (`initialize` → `notifications/initialized` → `tools/list`) at startup, exposes the tool list to the slash-command grammar, and dispatches `tools/call` when `/mcp <server>.<tool>` is invoked.

## Non-interactive (`--print`)

```sh
testagent --print --output-format stream-json "summarize the diff"
```

Emits `{"type":"system","subtype":"init",...}` then `{"type":"assistant",...}` then `{"type":"result",...}` — three frames matching Claude Code's stream-json shape.

## License

MIT.
