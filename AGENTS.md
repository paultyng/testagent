# AGENTS.md

Context for AI agents working on this repo. (Cursor, Claude Code, and others read this file.)

## What is testagent?

A fake CLI agent for testing orchestration tooling that drives real coding agents (Claude Code, Codex, Gemini, GitHub Copilot CLI, etc.). Runs as an interactive PTY process; emits the same kinds of terminal artifacts (boxed tool-use blocks, streaming text, in-place updates) and the same protocol traffic (HTTP hooks, MCP JSON-RPC) as a real agent — without calling any LLM API.

v1 ships a drop-in fake for Claude Code: argv compatibility for the flags orchestrators commonly emit, HTTP hooks (`UserPromptSubmit` / `PostToolUse` / `Stop` / `SessionEnd`), an MCP HTTP client that handshakes and dispatches `tools/call`, `--print --output-format stream-json` for non-interactive callers, a slash-command grammar for driving UI primitives interactively, and lipgloss/glamour rendering for plausible-shape output.

## Future phases

| Phase | What |
|---|---|
| 3+ | Vendor-neutral refactor; per-agent-type subcommands + packages (see *Future conventions*); adapters for codex, gemini, copilot, plus tier-2 (aider, amp, q, goose, crush); conformance tests against captured real-vendor traffic |
| release | `goreleaser` cross-OS binaries (darwin/linux/windows × amd64/arm64) + signed releases |

## Design conventions

- **Schema types are duplicated, not imported.** `Settings` and `MCPConfig` mirror Claude Code's on-disk shapes. testagent stays self-contained.
- **Stdlib-first; deps are deliberate.** Each non-stdlib dep is justified in the commit message that adds it (`mark3labs/mcp-go`, `lipgloss`, `glamour`).
- **Conventional Commits.** One commit per phase. Each phase's commit leaves the tree buildable and tested.
- **Tests:** `t.Parallel()`, table-driven, real `httptest`/`exec`-driven integration over mocks where possible (see `e2e_test.go`). Fixtures in `testdata/`.

## Future conventions (apply when phase 3+ lands)

- **One subcommand per emulated agent type**: `testagent claude ...`, `testagent codex ...`, `testagent gemini ...`, etc. The current bare invocation is the implicit `claude` mode.
- **One package per agent type** under `internal/agents/<vendor>/` containing the vendor's argv shape, payload encoders, and any vendor-specific quirks. Shared engine in `internal/`.
- **Package boundaries** worth carving as the codebase grows: `internal/slash` (command grammar + dispatcher), `internal/hooks` (HTTP hook sender), `internal/mcp` (MCP client), `internal/render` (lipgloss/glamour wrappers). Currently all in `package main` for v1 simplicity.

## Fixtures

Real-Claude protocol shapes are captured from a real Claude session run against an orchestrator that records hook POSTs and MCP JSON-RPC frames. Captures live under `testdata/captures/` and are **gitignored** (they contain real session content). Sanitized, OSS-safe fixtures are committed under `testdata/fixtures/` as phases that need them are authored.
