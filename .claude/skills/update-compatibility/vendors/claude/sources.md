# Sources — claude vendor

## Doc URLs

| Page | URL |
|------|-----|
| CLI reference (flags) | `https://code.claude.com/docs/en/cli-reference` |
| Commands (slash) | `https://code.claude.com/docs/en/commands` |
| Interactive mode (REPL behaviors) | `https://code.claude.com/docs/en/interactive-mode` |
| Skills (bundled list) | `https://code.claude.com/docs/en/skills` |
| Hooks (event reference) | `https://code.claude.com/docs/en/hooks` |
| Settings (schema) | `https://code.claude.com/docs/en/settings` |
| MCP (server commands) | `https://code.claude.com/docs/en/mcp` |

## gh API recipes

```sh
# Latest release version
gh api "repos/anthropics/claude-code/releases?per_page=1" | jq -r '.[0].tag_name'

# Recent releases (for changelog review)
gh api "repos/anthropics/claude-code/releases?per_page=5" | jq -r '.[] | "\(.tag_name)  \(.published_at)"'
```

## Local binary recipes

```sh
# Version
claude --version

# Root help (not exhaustive — see cli-reference docs)
claude --help

# Dump testagent's claude subcommand help (the contract artifact)
task dumpcli:claude   # writes compat/claude.cli.txt
task dumpcli:root     # writes compat/root.cli.txt
```

## Common pitfalls

- **`--help` is incomplete.** The CLI reference docs explicitly note "claude --help does not list every flag". Always fetch `cli-reference` for the full list.
- **JS-rendered pages.** If `WebFetch` returns an empty body or a loading message, retry once with the same URL. If it still fails, note `TODO: source` in the affected rows.
- **Bundled skills.** The commands page marks bundled skills with `[Skill]`. These always land at `not relevant` in the matrix — testagent has no model.
- **MCP prompt commands.** Format is `/mcp__<server>__<prompt>`. These are dynamically contributed by connected servers and are not static matrix rows.
- **Version pin.** Pin the upstream Claude Code version in the COMPATIBILITY.md header so reviewers know which release the matrix was validated against.
