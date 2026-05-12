# testagent

A fake `claude` / `codex` CLI. Deterministic output. No model, no network, no tokens. Iterate locally; run in CI without an API key.

![testagent claude in action](demo/claude.gif)
![testagent codex in action](demo/codex.gif)

## Use cases

**Local hook development.** A `Type="http"` or `Type="command"` hook fires the same JSON payload real Claude Code fires. Wire it to your handler, script a `/fake-tool` + `/fake-tool-result` sequence, watch the payload land. Sub-second iterations.

**Deterministic CI.** Drop testagent in wherever your pipeline shells out to `claude` or `codex`. Argv-compatible, `--print --output-format stream-json` emits the same frame shapes a real run would. No API key in the CI secret store, no rate-limit flakes, same bytes every run.

## Install

```sh
go install github.com/paultyng/testagent@latest
```

### As a Go tool module (Go 1.24+)

Pin testagent alongside your other dependencies so CI uses the same version without a separate install step:

```sh
go get -tool github.com/paultyng/testagent@latest
go tool testagent claude --help
```

## Example

Declare a `Type="command"` hook in `settings.json`. It pipes the event JSON to a shell command's stdin — here, a one-liner that captures the payload to disk:

```json
{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"jq . > tool-use.json"}]}]}}
```

Run testagent with a scripted tool-use cycle:

```sh
testagent claude --settings settings.json <<'EOF'
/fake-tool read_file {"path":"main.go"}
/fake-tool-result {"content":"package main"}
/exit
EOF
```

Inspect what your hook handler received:

```sh
jq . tool-use.json
# {
#   "hook_event_name": "PostToolUse",
#   "session_id": "...",
#   "tool_name": "read_file",
#   "tool_input": {"path": "main.go"},
#   "tool_response": {"content": "package main"},
#   ...
# }
```

Same shape Claude Code fires. Sub-second feedback. No model, no network, no API key.

## What it covers

See [COMPATIBILITY.md](COMPATIBILITY.md) for the per-vendor matrix of flags, slash commands, hook events, hook handler types, and REPL behaviors.

## What it doesn't cover

- **No model.** Output is scripted: prompt echoes, `/fake-tool` blocks you supply, fixed stream-json frames. testagent doesn't generate text.
- **Not every Claude hook event yet.** Currently fires `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `PreCompact`, `PostCompact`. `Notification` and `SubagentStop` aren't modeled.
- **Not every Claude hook handler type.** `Type="http"` and `Type="command"` ship. `Type="agent"` requires a model and is out of scope.
- **No MCP server fake.** testagent is an MCP client — it connects to a real server and dispatches `tools/call`. It doesn't fake the server side.

## License

MIT.
