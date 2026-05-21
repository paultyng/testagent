# Sources — upstream config corpus

The drift skill pulls canonical config-example fenced blocks from
these sources. All three vendors publish a markdown-native source
(llms.txt or a GitHub README); no HTML→markdown fallback is wired.

## Vendor source map

| Vendor | Format | Source | Fetcher |
|---|---|---|---|
| claude | llms.txt + linked docs pages | https://code.claude.com/llms.txt | curl markdown; follow links containing "hooks" or "mcp" |
| cursor | llms.txt + linked docs pages | https://cursor.com/llms.txt | curl markdown; follow links containing "hooks" or "mcp" |
| codex | raw README | gh api repos/openai/codex/contents/README.md -H 'Accept: application/vnd.github.raw' | inline fenced blocks |

## Per-vendor extraction notes

### claude

llms.txt is a flat list of doc URLs. Fetch llms.txt; grep for URLs
containing "hooks" or "mcp"; curl each linked page (also markdown);
extract fenced ```json code blocks; cross-check against
`testdata/upstream-examples/claude/`.

### cursor

Same shape as claude. Cursor's llms.txt is the catalog of doc pages;
hooks examples live under `/docs/hooks`, MCP under `/docs/context/mcp`.
Extract fenced ```json blocks.

### codex

The repo README at `openai/codex` contains inline fenced ```toml
blocks for `[hooks.<event>]` and `[mcp_servers.<name>]` shapes.
Re-fetch via `gh api` whenever the drift skill runs.
