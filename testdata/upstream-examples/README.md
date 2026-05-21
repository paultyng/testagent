# Upstream-docs example corpus

This directory contains **positive examples only** — config snippets taken directly from vendor documentation that pass `--strict` validation today. They serve as regression fixtures: if a future testagent change causes a previously-passing example to fail, the test suite surfaces it immediately.

## Directory structure

```
testdata/upstream-examples/
  claude/    settings.json (hooks) and --mcp-config examples
  codex/     config.toml examples
  cursor/    .cursor/hooks.json and .cursor/mcp.json examples
```

## The `.source` sibling convention

Every fixture file has a sibling `.source` file with exactly two lines:

```
url: <canonical docs URL>
verified: <ISO date>
```

The `url:` line points to the upstream docs page the example was extracted from. The `verified:` date records when the example was last confirmed to pass `--strict`.

## Adding a new example

1. Copy the example verbatim from the upstream docs page.
2. Probe it: `testagent <vendor> validate [--settings|--mcp-config|--workspace] /path/to/fixture --strict`
3. If exit 0, drop the file in the appropriate subdirectory with a kebab-case name (e.g. `pre-tool-use-bash-policy.toml`).
4. Write the `.source` sibling with `url:` and `verified:` lines.
5. Run the per-vendor test to confirm it passes: `go test ./... -run TestUpstreamExamples`

## Curation rule

Examples that fail `--strict` today are **dropped, not relaxed**. The gap between the vendored corpus and live docs is surfaced by the Phase 3 drift workflow, which is the right place to notice missing coverage. Bumping testagent's own `--strict` allowlist to cover new fields or events is a separate `update-compatibility`-style change, gated by user judgment.

## Dropped examples (curation note)

The following upstream-docs examples were probed and dropped because they use fields or types not in testagent's `--strict` allowlist. Phase 3 drift detection will surface these gaps.

### Claude (`code.claude.com/docs/en/hooks`)

| Example | Dropped field/type | Reason |
|---|---|---|
| PreToolUse Bash block-rm | `"if"`, `"args"` | `Hook.if` and `Hook.args` are not in testagent's Hook struct |
| HTTP hook with bearer | `"allowedEnvVars"` | `Hook.allowedEnvVars` is not in testagent's Hook struct |
| MCP Tool hook | `"type": "mcp_tool"`, `"server"`, `"tool"`, `"input"` | `mcp_tool` is not in `knownClaudeHookTypes`; associated fields unknown |
| Plugin Scripts | `"args"` (top-level on Hook), `"description"` (top-level on Settings) | Neither field is in testagent's structs |

### Codex (`developers.openai.com/codex/hooks`)

| Example | Dropped field/type | Reason |
|---|---|---|
| PreToolUse (PascalCase) + statusMessage | `"PreToolUse"` event name, `statusMessage` key | Codex docs show PascalCase event names; testagent's allowlist uses snake_case (`pre_tool_use`). `statusMessage` is not in `HookEntry`. These are schema drift candidates for `update-compatibility`. |
| PostToolUse (PascalCase) + statusMessage | Same as above | Same reason |
| Figma MCP server (`bearer_token_env_var`, `http_headers`) | `bearer_token_env_var`, `http_headers` | Not in testagent's `MCPServer` struct |

### Cursor (`cursor.com/docs/hooks`)

| Example | Dropped field/type | Reason |
|---|---|---|
| Comprehensive hooks (sessionStart/sessionEnd/preCompact/beforeSubmitPrompt/beforeTabFileRead/afterTabFileEdit) | Six event names | `sessionStart`, `sessionEnd`, `preCompact`, `beforeSubmitPrompt`, `beforeTabFileRead`, `afterTabFileEdit` are not in `knownCursorEvents` |
