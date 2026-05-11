---
name: publish-release
description: >
  Drive cutting a new testagent release end-to-end: analyze the
  surface diff against the bump table in RELEASING.md, propose a
  version, draft three-bucket release notes, tag, watch CI, then
  publish curated notes.
when_to_use:
  - "publish release"
  - "cut a release"
  - "tag release"
  - "ship v0.x"
  - "release testagent"
  - "next version"
---

# publish-release

Execute the release process documented in [RELEASING.md](../../../RELEASING.md).
RELEASING.md is the **policy source-of-record**: semver scope,
the bump table keyed to testagent's public surface, two runbooks,
recovery branches. This skill is the **executor**: it cites
RELEASING.md sections by anchor, performs each step, and gates on
the two decisions a human must own (version number, release-notes
copy).

## 1. Preflight

```bash
git checkout main
git pull --ff-only
git status --porcelain     # must be empty
task ci                    # full local build + lint + test
gh auth status             # GH_TOKEN scope: repo
```

Identify the previous tag:

```bash
PREV=$(git describe --tags --abbrev=0)
```

If any preflight step fails, **stop and report**. Do not proceed —
release should always cut from a clean, green main.

## 2. Surface diff + bump analysis

Run the surface-touching diff from [RELEASING.md § Pre-tag checklist](../../../RELEASING.md):

```bash
git log $PREV..HEAD --oneline
git diff $PREV..HEAD -- main.go cmd/ internal/slash/slash.go \
  internal/hooks/hooks.go cmd/claude/print.go
```

Group commit subjects by Conventional Commits type (`feat:`,
`fix:`, `refactor:`, `chore:`, `docs:`, `ci:`, `test:`,
`BREAKING CHANGE:`). For each surface-touching change, map to the
bump table at [RELEASING.md § Semver policy](../../../RELEASING.md).

- Any `BREAKING CHANGE:` footer or `!` in the subject → **major**
  (but see the v0.x clamp below).
- New flag / slash command / hook event / public surface → **minor**.
- Pure bug fix / internal refactor / docs / test → **patch**.

**v0.x clamp**: until v1.0.0, the project's convention is to clamp
*everything* to patch unless the user explicitly overrides. New
features still go in, just packaged as `v0.Y.Z+1` not `v0.Y+1.0`.
This trades semver-strictness for release cadence and is documented
in RELEASING.md.

Output: proposed bump (`patch` / `minor` / `major`) with file-and-
line evidence per category.

## 3. Version proposal — HUMAN GATE

Compute the next version: prev tag + proposed bump. Present:

```
PREV: v0.2.1
PROPOSED: v0.2.2 (patch)
JUSTIFICATION: <one-liner citing the highest-impact change>
```

**Wait for explicit confirmation** before proceeding. Honor the
`confirm-before-implementing` rule. The user may override the
version (e.g. promote a patch to a minor for visibility) — accept
the override and continue.

## 4. COMPATIBILITY.md drift check

```bash
task dumpcli:claude
task dumpcli:codex
```

If either dump differs from what `COMPATIBILITY.md` claims, **flag
before tagging** and hand off to the appropriate research skill:

- Claude drift → invoke `research-claude-coverage`
- Codex drift → invoke `research-codex-coverage`

Do **not** auto-fix matrix rows from this skill. Wait for the user
to drive the matrix update (or explicitly waive the check) before
returning here.

## 5. Release notes draft — HUMAN GATE

Generate three-bucket Markdown per [RELEASING.md § Writing release
notes](../../../RELEASING.md):

```markdown
**<one-paragraph user-visible narrative — what changed and why
it matters>**

## Breaking changes
- <bullet> (#NN)

## New
- <bullet> (#NN)

## Fixed
- <bullet> (#NN)
```

Rules:
- Group `BREAKING CHANGE:` commits → Breaking; `feat:` → New;
  `fix:` → Fixed. Drop `chore:`, `docs:`, `ci:`, `test:`,
  `refactor:` from the curated section — they live in the auto
  commit-list appendix only.
- Cite the PR number (`(#NN)`) extracted from each commit subject.
- Lead bullets with user-visible effect, not implementation
  (e.g. "OSC 11 reply no longer wedges the input reader" not
  "migrated to bubbletea v2").
- Omit empty buckets entirely.
- Narrative cap: 200 words. Bullet copy: terse imperative.

Present the draft. **Wait for explicit confirmation**.

## 6. Tag + push

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

The tag push triggers `.github/workflows/release.yml` →
goreleaser. The annotation message can be brief — the curated
release-notes copy goes onto the GitHub Release page in step 8.

## 7. Watch CI

```bash
RUN=$(gh run list --workflow=release.yml --branch=vX.Y.Z --limit=1 \
  --json databaseId -q '.[0].databaseId')
gh run watch $RUN --exit-status
```

On failure, surface the relevant log slice and hand off to
[RELEASING.md § Runbook B (local fallback)](../../../RELEASING.md):

```bash
git fetch --tags
git checkout vX.Y.Z
export GH_TOKEN=$(gh auth token)
task release:local
```

## 8. Publish curated notes

After goreleaser publishes the auto-generated commit list, prepend
the curated three-bucket draft from step 5:

```bash
EXISTING=$(gh release view vX.Y.Z --json body -q .body)
CHANGELOG=$(printf '%s\n' "$EXISTING" | sed -n '/^## Changelog/,$p')
PAYLOAD=$(printf '%s\n%s' "$CURATED_DRAFT" "$CHANGELOG")
gh release edit vX.Y.Z --notes "$PAYLOAD"
```

Verify the GitHub Release page renders both the narrative and the
auto changelog.

## 9. Verify

```bash
gh release view vX.Y.Z
```

Confirm:
- All eight cross-platform artifacts uploaded (linux/darwin/windows
  × amd64/arm64 + source tarball + checksums.txt)
- `prerelease=false` for stable cuts; `prerelease=true` for tags
  with a `-` suffix (goreleaser's `prerelease: auto`)
- `go install github.com/paultyng/testagent@vX.Y.Z` resolves

Report the release URL and any artifacts that were unexpectedly
missing.

## Hard constraints

- **No tag push without explicit version confirmation** (step 3).
- **No release notes published without explicit copy confirmation**
  (step 5).
- **No matrix auto-fixes** — drift detection only; hand off to
  `research-*-coverage` skills.
- **No edits to `RELEASING.md`** from this skill. Policy changes
  go through normal PR review.
- If any step's command fails, stop and report rather than retrying
  blindly — release state is shared infrastructure.
