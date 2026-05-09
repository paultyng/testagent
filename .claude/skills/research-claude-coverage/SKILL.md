---
name: research-claude-coverage
description: >
  Research the current Claude Code release and update the compat matrix
  (COMPATIBILITY.md) with accurate flags, slash commands, and REPL behaviors.
when_to_use:
  - "update compat"
  - "check claude coverage"
  - "sync compatibility"
  - "new claude version"
  - "compat drift"
  - "research claude"
---

# research-claude-coverage

Use this skill to refresh `COMPATIBILITY.md` after a Claude Code release or when the compat matrix may have drifted from upstream.

## When to invoke

- A new Claude Code version is released (`gh api repos/anthropics/claude-code/releases?per_page=1`).
- `task dumpcli:claude` exits non-zero (flag drift detected).
- A contributor adds a new flag or command to testagent and the matrix row is missing.
- Periodic coverage check (recommended: after each minor release series).

## Sources — read in this priority order

1. `compat/claude.cli.txt` — committed contract artifact from `task dumpcli:claude`. Authoritative for which testagent flags exist.
2. `COMPATIBILITY.md` — current state of record; start here to avoid re-researching unchanged rows.
3. `claude --version` — pin the upstream version you researched against.
4. Docs at `code.claude.com/docs/en/`:
   - `cli-reference` — complete flag table (canonical; some flags not in `--help`)
   - `commands` — all slash commands, built-in vs bundled-skill vs plugin-MCP
   - `interactive-mode` — keyboard shortcuts, `!`-shell, `@`-mention, vim mode
   - `skills` — bundled-skill list
   - `hooks` — hook event reference
   - `settings` — settings schema
   - `mcp` — MCP command format (`/mcp__<server>__<prompt>`)
5. `gh api repos/anthropics/claude-code/releases?per_page=5` — version history and release notes.

## Common pitfalls

- Some doc pages render client-side JavaScript. If `WebFetch` returns empty or a loading spinner, retry once; if still empty, leave a `TODO: source` note in the row rather than guessing.
- `claude --help` does NOT list every flag (the docs say so explicitly). Always cross-reference with the `cli-reference` page.
- MCP prompt commands use the format `/mcp__<server>__<prompt>` — these are dynamic and not rows in the matrix.
- Bundled skills appear in the commands table marked **[Skill]**. They **always land at `not relevant`** in testagent's matrix because testagent has no model and cannot execute them.

## Output format

Produce or update `COMPATIBILITY.md` at the repo root using the skeleton in `report-template.md`. Hard cap: **1500 words** for the document body (excluding the legend and header).

### Five-state legend

| Symbol | Meaning |
|--------|---------|
| `✓ supported` | testagent implements this feature |
| `partial` | partially implemented (note what's missing) |
| `accepted` | flag/command accepted without error but silently ignored |
| `not relevant` | not applicable to a fake agent (e.g. TUI-internal, model-dependent) |
| `✗ planned` | not yet implemented; tracked in an issue |

### Naming-collision callout

**Always include this callout** in the slash-commands section when covering Claude:

> **Naming collision:** Claude Code's `/mcp` opens a server-management UI. testagent uses `/mcp-call` for tool dispatch to avoid colliding with this. Claude Code's `/exit` exits the CLI; testagent's `/exit [code]` does the same. Claude Code's `/help` shows help; testagent's `/help` does the same.

### Grouping for slash commands

Group slash-command rows by origin:
1. **Built-in** — coded into the CLI
2. **Bundled skill** — marked [Skill] in the docs; always `not relevant` for testagent
3. **Plugin/MCP-contributed** — dynamic; always `not relevant` for testagent

### Scale targets (Claude vendor)

The matrix should cover approximately:
- ~20 flag rows (alphabetical by long name, short flag inline)
- ~15 slash-command rows
- ~10 REPL-behavior rows

## Issue linking (mandatory)

Every `✗ planned` and `partial` row must link to its tracking issue when one
exists. Format: `Tracked in [#N](https://github.com/paultyng/testagent/issues/N)`
in the Notes column. Discover candidates via `gh issue list --repo
paultyng/testagent --state open` and match by feature description (e.g.
PreCompact hook → #12, `!`-shell prefix → #17). If no issue exists for a
`✗ planned` row, note `Tracked in: TODO open issue` so a follow-up can be
filed.

A `not relevant` row that *does* have an open issue (because someone proposed
adding it to testagent) should be flipped to `✗ planned` with the link.

## Hard constraint

Do not invent flag or command names. If a page is unreachable after one retry, leave the row as `| \`--flag\` | TODO: source | — |`. Do not file issues or post comments as part of this skill run.
