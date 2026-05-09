# testagent

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

## Hooks

When `--settings` declares hook URLs, testagent POSTs Claude-Code-shaped event bodies on the appropriate lifecycle moments: `UserPromptSubmit` per user input, `Stop` after each assistant response, `PostToolUse` per `/tool` invocation, `SessionEnd` on shutdown.

## MCP

When `--mcp-config` lists servers, testagent (via `mark3labs/mcp-go`) performs the standard handshake (`initialize` → `notifications/initialized` → `tools/list`) at startup, exposes the tool list to the slash-command grammar, and dispatches `tools/call` when `/mcp <server>.<tool>` is invoked.

## Non-interactive (`--print`)

```sh
testagent --print --output-format stream-json "summarize the diff"
```

Emits `{"type":"system","subtype":"init",...}` then `{"type":"assistant",...}` then `{"type":"result",...}` — three frames matching Claude Code's stream-json shape.

## License

MIT.
