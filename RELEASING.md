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

Two runbooks, both producing identical artifacts (they share
`.goreleaser.yaml`). Pick one; recovery instructions below if a CI
release stalls partway.

### Prerequisites (both paths)

- [`goreleaser`](https://goreleaser.com/install/) installed for local —
  `brew install goreleaser` or
  `go install github.com/goreleaser/goreleaser/v2@latest`.
  Not needed if you're confident CI will handle it.
- `gh auth status` shows authenticated.
- `task ci` passes locally.
- Pre-tag checklist above is done; bump category decided.

### Runbook A — CI path (preferred)

1. **Pull latest main and confirm it's clean:**
   ```sh
   git checkout main && git pull --ff-only
   git status   # working tree must be clean
   ```
2. **Tag the commit:**
   ```sh
   git tag v0.1.0
   ```
3. **Push the tag:**
   ```sh
   git push origin v0.1.0
   ```
4. **Watch the workflow:**
   ```sh
   gh run watch     # or: gh run list --workflow=release.yml
   ```
   The `Release` workflow validates the tag is semver-2.0, runs
   goreleaser, and uploads cross-OS archives + checksums to a new
   GitHub release.
5. **Curate the release notes** on the GitHub release page (see
   *Writing release notes* below). The workflow leaves the
   auto-generated commit list in the body; prepend the curated
   summary on top.

### Runbook B — Local release

Use this when CI minutes are exhausted, the workflow is disabled, or
you want to verify a release locally before pushing.

1. **Pull latest main and confirm it's clean:**
   ```sh
   git checkout main && git pull --ff-only
   git status   # working tree must be clean
   ```
2. **Auth:**
   ```sh
   export GH_TOKEN=$(gh auth token)
   ```
3. **Tag and push:**
   ```sh
   git tag v0.1.0
   git push origin v0.1.0
   ```
4. **Run goreleaser locally:**
   ```sh
   task release:local
   ```
   The task validates `goreleaser` is on PATH, `GH_TOKEN` is set, and
   the current tag is semver-2.0, then runs
   `goreleaser release --clean` against the tag. Artifacts land in
   `dist/` and are uploaded to the GitHub release for the tag.
5. **Curate the release notes** on the GitHub release page (see
   *Writing release notes* below).

### Recovery — tag pushed but no release published

If you pushed a tag expecting Runbook A and the workflow didn't run
(disabled, minutes exhausted, etc.) or failed before goreleaser
finished:

1. **Check the workflow state:**
   ```sh
   gh run list --workflow=release.yml --limit 3
   ```
2. **If no run started**, switch to the local path. The tag already
   exists on the remote, so just:
   ```sh
   git fetch --tags
   git checkout v0.1.0
   export GH_TOKEN=$(gh auth token)
   task release:local
   ```
   goreleaser creates a new GitHub release for the tag.
3. **If a run started but failed mid-flight** (a partial GitHub
   release may exist), delete it first so goreleaser can re-upload
   from a clean slate:
   ```sh
   gh release delete v0.1.0 --yes --cleanup-tag=false
   git checkout v0.1.0
   export GH_TOKEN=$(gh auth token)
   task release:local
   ```
   `--cleanup-tag=false` keeps the tag intact so the same `v0.1.0`
   gets the artifacts.
4. **If the tag itself is wrong** (e.g., points at the wrong
   commit), delete and re-tag:
   ```sh
   git push origin :refs/tags/v0.1.0   # delete on remote
   git tag -d v0.1.0                    # delete locally
   gh release delete v0.1.0 --yes --cleanup-tag=false 2>/dev/null || true
   git tag v0.1.0                       # re-tag at HEAD
   git push origin v0.1.0
   # then Runbook A or B
   ```

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
