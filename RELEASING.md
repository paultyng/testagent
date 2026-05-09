# Releasing testagent

testagent ships cross-OS binaries via `goreleaser`. Tag-driven CI and a
local fallback both produce identical artifacts.

## Semver policy

testagent follows [Semantic Versioning](https://semver.org/) starting at
**v1.0.0**. Releases below v1.0.0 (the v0.x series) may change CLI flags
and hook payload shapes without a major bump — adopt them only if you can
track each release.

State transitions in [COMPATIBILITY.md](COMPATIBILITY.md) drive the
version-bump decision:

| Surface | Breaking | Additive | Non-breaking |
|---|---|---|---|
| CLI flags | Removing or renaming a `✓ supported` flag | Adding a new flag (any state) | Changing the default value of an internal flag |
| Slash commands | Removing or renaming a `✓ supported` command | Adding a new command (any state) | Refactoring command rendering |
| Hook payload fields | Removing or renaming a field on any `✓ supported` event | Adding an optional field | Changing log verbosity or `--verbose` formatting |
| Hook event names | Removing a `✓ supported` event | Adding a new event (e.g. `PostCompact`) | — |
| MCP protocol version | Dropping a previously-advertised `protocolVersion` | Advertising an additional version | Internal MCP client refactor |
| `internal/` packages | N/A — not public API | N/A | All changes |

Specifically:

- `✓ supported` → removed = **major** (or **patch** while pre-1.0)
- `✗ planned` → `✓ supported` = **minor** (or **patch** while pre-1.0)
- Bug fix within a `✓ supported` surface = **patch**
- Internal refactor with no surface change = **patch**

## Cutting a release

### Prerequisites

- [`goreleaser`](https://goreleaser.com/install/) installed
  (`brew install goreleaser` or
  `go install github.com/goreleaser/goreleaser/v2@latest`).
- `task ci` passes locally (lint, test, build).
- `gh auth status` shows authenticated.

### CI path (preferred)

Push a `vX.Y.Z` tag. GitHub Actions picks it up via
`.github/workflows/release.yml`, runs goreleaser, and uploads the
artifacts to the GitHub release.

```sh
git tag v0.1.0
git push origin v0.1.0
```

### Local fallback (when CI minutes are exhausted or unavailable)

Both paths share `.goreleaser.yaml`, so the artifacts are identical —
they differ only in WHERE the upload auth comes from.

```sh
export GH_TOKEN=$(gh auth token)
git tag v0.1.0
git push origin v0.1.0
task release:local
```

`task release:local` validates `goreleaser` is on PATH and `GH_TOKEN`
is set, then runs `goreleaser release --clean` against the current tag.
Artifacts land in `dist/` and are uploaded to the GitHub release for the
tag.

## Signing

Not yet. v0.x releases are unsigned. Cosign / Sigstore keyless signing
is planned for v1.1+ once the surface stabilises and a signing-key story
is committed to.

## Out of scope (for now)

- Homebrew tap / formula
- Scoop bucket
- winget submission
- Apple notarization (separate apple-id + password flow, requires macOS runner)
- Linux package repos (deb/rpm)
- SBOM generation
