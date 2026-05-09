# Releasing testagent

testagent ships cross-OS binaries via `goreleaser`. Tag-driven CI and a
local fallback both produce identical artifacts.

## Semver policy

testagent follows [Semantic Versioning](https://semver.org/) starting at
**v1.0.0**. Releases below v1.0.0 (the v0.x series) may change the public
interface without a major bump — adopt them only if you can track each
release.

The bump is driven by **the binary's public interface**, not by edits to
[COMPATIBILITY.md](COMPATIBILITY.md). The matrix is documentation; the
binary is source of truth. The interface comprises:

1. **CLI flags** — root persistent flags + per-subcommand flags
   (visible via `testagent --help` and `testagent <subcommand> --help`).
2. **Slash commands** — the names + argument shapes the slash dispatcher
   accepts (`internal/slash/slash.go`).
3. **Hook event names** — the constants in `internal/hooks/hooks.go`
   (`UserPromptSubmit`, `Stop`, etc.).
4. **Hook payload fields** — the JSON tags on event body structs.
5. **MCP `protocolVersion`** advertised by the client.
6. **`--print` output formats** and their JSON / stream-JSON shapes.

Bump table (apply per change; the highest-impact one wins for the release):

| Change | Bump (post-1.0) | Bump (pre-1.0) |
|---|---|---|
| Remove or rename a flag, slash command, hook event, payload field | **major** | patch |
| Drop a previously-advertised MCP `protocolVersion` | **major** | patch |
| Change a flag's accepted-value shape (e.g. `--output-format` enum) in a non-superset way | **major** | patch |
| Add a new flag, slash command, hook event, optional payload field | **minor** | patch |
| Advertise an additional MCP `protocolVersion` | **minor** | patch |
| Add a new `--print` output-format value | **minor** | patch |
| Bug fix within an existing surface (output matches what was already documented) | **patch** | patch |
| Default-value change on an existing flag (rare; treat as breaking unless purely internal) | **major** | patch |
| Anything in `internal/`, `cmd/<vendor>/` private logic, `Taskfile.yaml`, demo, docs, CI, tests | none (not public API) | none |

`COMPATIBILITY.md` row state changes (`✗ planned` → `✓ supported`, etc.)
are *consequences* of the above — update the matrix in the same PR that
flips the binary's behavior. The matrix should never lag the surface in
a published release.

## Pre-tag checklist

Before pushing a release tag, eyeball the diff against the previous tag:

```sh
git diff vPREV..HEAD -- main.go cmd/ internal/slash/slash.go internal/hooks/hooks.go cmd/claude/print.go
```

Anything in the bump table that touches any of those files is the
candidate bump. Pick the highest-impact category and tag accordingly.

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

## Writing release notes

goreleaser auto-generates a flat commit list from `^feat:` / `^fix:` /
`^refactor:` commits (configured in `.goreleaser.yaml`'s `changelog`
filter). That list is honest but unstructured; before publishing, edit
the GitHub release page to **prepend a curated summary** in three
buckets that orchestrators care about. Keep the auto-generated commit
list at the bottom — it's a useful audit trail.

### Structure

```markdown
## Breaking changes

- Removed `--foo` flag; orchestrators previously using it should switch
  to `--bar` ([#123](https://github.com/paultyng/testagent/issues/123)).
- `Stop` hook payload renamed `last_message` → `last_assistant_message`;
  update parsers (PR [#456](https://github.com/paultyng/testagent/pull/456)).

## New

- `/link <url> [text]` slash command emits an OSC 8 hyperlink so
  orchestrators can script clickable links in tests
  ([#24](https://github.com/paultyng/testagent/issues/24),
  PR [#27](https://github.com/paultyng/testagent/pull/27)).
- `--stream-delay` flag overrides per-token echo cadence (default 30ms).

## Fixed

- `runPrint` now honors `--resume` when firing `SessionStart` (was
  always emitting `source="startup"`)
  ([#25](https://github.com/paultyng/testagent/issues/25),
  PR [#26](https://github.com/paultyng/testagent/pull/26)).
```

### Conventions

- **Order matters.** Breaking changes first (consumers must read them);
  new features second; fixes last.
- **Omit empty buckets.** A patch release that's pure bug fixes has only
  a `## Fixed` section.
- **One bullet per change**, not per commit. If three commits land one
  feature, write one bullet citing the PR.
- **Cite the issue + PR**, in that order, with full markdown links. For
  changes that landed without an issue, just the PR. For multi-PR
  features, list all PRs.
- **Lead with the user-visible effect**, not the implementation. "Banner
  now shows the emulator type prefix" beats "added `Emulator` field to
  `engine.Globals`."
- **Migration hints under breaking changes.** If a flag was renamed,
  show the old → new mapping. If a payload field was renamed, mention
  the parser update.
- **Pre-1.0 caveat** for v0.x releases that contain breaking changes:
  add a one-line reminder at the top that the v0.x line is not
  semver-stable, and pin to a specific version if your tests need
  determinism.

### Where to write them

Edit the GitHub release page directly after goreleaser publishes
(it creates the release with `prerelease: auto` honored from the tag).
The auto-generated commit list goes below your curated three-bucket
summary. Don't re-run goreleaser to amend notes — just edit the
release.

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
