# AGENTS.md

Context for AI agents working on this repo. (Cursor, Claude Code, and others read this file.)

## What is testagent?

A fake CLI agent for testing orchestration tooling that drives real coding agents (Claude Code, Codex, Gemini, GitHub Copilot CLI, etc.). Runs as an interactive PTY process; emits the same kinds of terminal artifacts (boxed tool-use blocks, streaming text, in-place updates) and the same protocol traffic (HTTP hooks, MCP JSON-RPC) as a real agent — without calling any LLM.

The product framing is **deterministic output for tests**, not "no LLM" as a virtue. Wiring a local LLM into a test harness is a valid choice for some workflows; testagent is explicitly not that. Every assertion-relevant byte (slash dispatch, hook payloads, MCP frames, stream-json shapes) is scripted by the user, not generated, so tests stay stable across runs.

v1 ships a drop-in fake for Claude Code: argv compatibility for the flags orchestrators commonly emit, HTTP hooks (`UserPromptSubmit` / `PostToolUse` / `Stop` / `SessionStart` / `SessionEnd`), an MCP HTTP client that handshakes and dispatches `tools/call`, `--print --output-format stream-json` for non-interactive callers, a slash-command grammar for driving UI primitives interactively (including `/restart` which simulates `/clear`- or `/compact`-style hook resets), and lipgloss-rendered plausible-shape output.

## Future phases

| Phase | What |
|---|---|
| 3+ | Vendor-neutral refactor; per-agent-type subcommands + packages (see *Future conventions*); adapters for codex, gemini, copilot, plus tier-2 (aider, amp, q, goose, crush); conformance tests against captured real-vendor traffic |
| release | `goreleaser` cross-OS binaries (darwin/linux/windows × amd64/arm64) + signed releases |

## Design conventions

- **Schema types are duplicated, not imported.** `Settings` and `MCPConfig` mirror Claude Code's on-disk shapes and live in `cmd/claude/settings.go` (vendor-specific, not shared with the engine).
- **Stdlib-first; deps are deliberate.** Each non-stdlib dep is justified in the commit message that adds it (`mark3labs/mcp-go`, `lipgloss`, `bubbletea`, `bubbles`, `go-isatty`, `spf13/cobra`).
- **Interactive vs non-interactive split.** TTY stdin → bubbletea TUI (`internal/engine/tui.go`, alt-screen, concurrent input during the thinking spinner). Piped stdin → scanner loop (`internal/engine/scanner.go`, line-based, inline rendering). `--print/-p` is a third path (`cmd/claude/print.go`, one-shot output formatter). The `mattn/go-isatty` check on `os.Stdin` is the TUI/scanner gate; e2e tests pipe stdin so they always hit the scanner path.
- **Conventional Commits.** One commit per phase. Each phase's commit leaves the tree buildable and tested.
- **Tests:** `t.Parallel()`, table-driven, real `httptest`/`exec`-driven integration over mocks where possible (see `e2e_test.go`). Fixtures in `testdata/`.
- **Debug output goes to stderr.** Verbose / debug logging (e.g. `--verbose` hook traces) is plain text, one event per line, never ANSI-styled — it gets grepped and piped. Stdout stays reserved for stream-json frames and TUI rendering.

## Layout

```
main.go                     # cobra root + bare-invocation default-to-claude
cmd/claude/                 # claude subcommand: vendor flags, Settings/MCPConfig, runPrint
cmd/codex/                  # stub subcommand, validates per-vendor cobra contract
internal/engine/            # Globals + Deps + Run; TUI + scanner + spinner + token-stream helper
internal/hooks/             # Sender (HTTP hook POSTs)
internal/mcp/               # Client (MCP HTTP handshake + tools/call)
internal/slash/             # Handler (slash-command grammar)
internal/render/            # lipgloss style tokens + intent helpers
e2e_test.go                 # builds the binary, pipes stdin, asserts behavior
Taskfile.yaml               # build / test / lint / ci / gen:demo / dumpcli:claude
```

The argv shape is `testagent [global-flags] <subcommand> [subcommand-flags]`. Bare invocation prepends `claude` so v0 scripts (no subcommand) keep working. Lone `--help` / `-h` routes to root help, not claude help.

## Future conventions (apply when phase 3+ lands)

- **More vendors** under `cmd/<vendor>/`: gemini, copilot, aider, amp, q, goose, crush.
- **Vendor-specific quirks** stay in their own subcommand package; engine stays vendor-neutral.

## Fixtures

Real-Claude protocol shapes are captured from a real Claude session run against an orchestrator that records hook POSTs and MCP JSON-RPC frames. Captures live under `testdata/captures/` and are **gitignored** (they contain real session content). Sanitized, OSS-safe fixtures are committed under `testdata/fixtures/` as phases that need them are authored.

## Demos

`demo/hero.tape` renders `demo/hero.gif` (the README hero) via [vhs](https://github.com/charmbracelet/vhs). The `demo/` directory holds **only** the hero — nothing else gets committed there.

When a PR changes user-visible behavior:

- **Re-render `demo/hero.gif`** if the change affects what the hero shows (banner, slash output, thinking animation, etc.).
- **For a focused per-PR animation**, paste it into the **PR description or a PR comment**. Layout: the rendered gif goes inline (visible — uploaded via GitHub's drag-and-drop attachment so reviewers see the animation immediately), and the tape source goes inside a `<details><summary>tape</summary>…</details>` collapse with a fenced ```` ```vhs ```` block. Do NOT commit the tape or gif to `demo/`. Per-PR demos are review aids, not project artifacts — they live with the PR thread and disappear when the branch is deleted.
