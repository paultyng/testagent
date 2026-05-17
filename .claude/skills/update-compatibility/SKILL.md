---
name: update-compatibility
description: Use when refreshing a vendor's section of COMPATIBILITY.md after an upstream release, when `task dumpcli:<vendor>` reports drift, when a contributor adds a flag or slash command not yet in the matrix, or for periodic coverage checks. Vendor is parsed from the trigger phrase. Triggers include "update claude compat", "research claude", "check claude coverage", "claude drift", "new claude version" — and the same patterns for "codex" and "cursor". Generic phrases ("compat drift", "sync compatibility") prompt for the vendor.
---

# update-compatibility

Refreshes one vendor's section of `COMPATIBILITY.md`. Replaces the per-vendor `research-{claude,codex,cursor}-coverage` skills that came before — shared conventions live here once; per-vendor source URLs, probing recipes, and matrix templates live under `vendors/<name>/`.

## Vendor dispatch

The trigger phrase names the vendor. Examples:

| Phrase | Vendor arg |
|--------|------------|
| "research codex" / "update codex compat" / "codex drift" | `codex` |
| "research claude" / "sync claude compatibility" | `claude` |
| "research cursor" / "update cursor compat" | `cursor` |
| "compat drift" (vendor unclear) | **ask the user** |

When the vendor is unambiguous, proceed without asking. When ambiguous, ask: *"Which vendor — claude, codex, or cursor?"*

## When to invoke

- A new upstream version is released.
- `task dumpcli:<vendor>` exits non-zero (flag drift detected). Today: `dumpcli:claude` and `dumpcli:codex` exist; `dumpcli:cursor` lands with the cursor adapter wiring.
- A contributor adds a new flag or command to the vendor stub and the matrix row is missing.
- Periodic coverage check (recommended: after each upstream minor-release series).

## Workflow

1. Resolve the vendor argument from the trigger (or ask).
2. Read `vendors/<vendor>/sources.md` for the authoritative source URLs and local probing recipes.
3. Run the local probes (subcommand `--help` dumps, `dumpcli:<vendor>` tasks).
4. Fetch each documented page in `sources.md`. Tolerate one retry per JS-rendered page; mark `TODO: source` on a stubborn fetch and continue rather than guess.
5. Cross-reference against the current `## <Vendor>` section in `COMPATIBILITY.md` — keep unchanged rows; add new rows for new features; mark removed rows as removed (or delete if no orchestrator likely depends on them).
6. Emit the updated section using `vendors/<vendor>/template.md` as the skeleton. Keep the other vendors' sections in `COMPATIBILITY.md` **untouched**.
7. Run `task dumpcli:<vendor>` (where available) to regenerate the contract artifact; commit both COMPATIBILITY.md and the regenerated `compat/<vendor>.cli.txt`.

## Shared conventions

### Five-state legend

| Symbol | Meaning |
|--------|---------|
| `✓ supported` | testagent implements this feature |
| `partial` | partially implemented (note what's missing) |
| `accepted` | flag/command accepted without error but silently ignored |
| `not relevant` | not applicable to a fake agent (e.g. TUI-internal, model-dependent) |
| `✗ planned` | not yet implemented; tracked in an issue |

### Output format

- Section starts with the upstream version pin: `**Upstream version researched:** <vendor> <version> (<date>)`.
- Tables are alphabetical within each group; short flags inline (e.g. `--print` / `-p`).
- Markdown tables only; no nested HTML.
- Hard cap: **1500 words** for the section body (excluding legend and per-vendor header). The legend lives once at the top of `COMPATIBILITY.md` — do not duplicate it per vendor.

### Naming-collision callouts

Every vendor section includes a naming-collision callout near the slash-commands table, calling out which testagent slash names override the vendor's defaults (`/mcp` vs `/mcp-call`, `/clear` vs `/restart clear`, etc.). The exact callout text lives in `vendors/<vendor>/template.md`.

### Issue linking (mandatory)

Every `✗ planned` and `partial` row must link to its tracking issue when one exists. Format: `Tracked in [#N](https://github.com/paultyng/testagent/issues/N)` in the Notes column. Discover candidates via `gh issue list --repo paultyng/testagent --state open` and match by feature description. If no issue exists for a `✗ planned` row, note `Tracked in: TODO open issue` so a follow-up can be filed.

A `not relevant` row that *does* have an open issue (because someone proposed adding it to testagent) should be flipped to `✗ planned` with the link.

### Per-vendor scale targets

| Vendor | Flags | Slash commands | REPL | Hooks |
|--------|-------|----------------|------|-------|
| Claude | ~20 | ~15 | ~10 | ~10 (per https://code.claude.com/docs/en/hooks; testagent's allowlist tracks the documented set) |
| Codex | ~15 (incl. key subcommand flags) | ~30 (alphabetical within groups) | ~8 | ~8 (`session_start` / `user_prompt_submit` / `pre_tool_use` / `post_tool_use` / `stop` / `pre_compact` / `post_compact` / `permission_request`) |
| Cursor | ~20 | ~9 | ~10 | ~21 (file-level / shell / MCP / subagent / lifecycle) |

## Hard constraints

- **Do not invent flag or command names.** If a page is unreachable after one retry, leave the row as `| \`--flag\` | TODO: source | — |`.
- **Do not file issues or post comments.** This skill produces a doc diff only.
- **Cursor: do not invoke `cursor agent` without `--help`.** Bare invocation launches the agent UI / makes an API call. Use `--help` and explicit subcommand helps only.
- **Pin the upstream version.** The matrix header must match what the local binary's `--version` prints. For Cursor specifically, that's a date-build identifier (`YYYY.MM.DD-<shortsha>`), not a semver.
- **Keep other vendors' sections untouched.** This skill operates on one section at a time.

## File layout

```
.claude/skills/update-compatibility/
├── SKILL.md                # this file
└── vendors/
    ├── claude/
    │   ├── sources.md      # claude-specific URLs, probing recipes, pitfalls
    │   └── template.md     # claude section skeleton
    ├── codex/
    │   ├── sources.md
    │   └── template.md
    └── cursor/
        ├── sources.md
        └── template.md
```

Per-vendor pitfalls (CODEX_HOME, cursor's interactive-launch hazard, claude's JS-rendered docs) live in the respective `vendors/<vendor>/sources.md`, not here. The cross-vendor common pitfall — JS-rendered docs needing a retry — is noted in Workflow step 4.
