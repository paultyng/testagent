# RELEASE_PLAN.md

Working document for issue #11. Delete before merge or after the PR is open.

## TL;DR

Four-file PR: add `RELEASING.md` (semver policy), wire `--version` into cobra root via ldflags, add `.goreleaser.yaml` (cross-OS, no signing in v1), and add `.github/workflows/release.yml` (tag-triggered). Extend `Taskfile.yaml` with a `release:local` task. First tag: `v0.1.0` — signals binaries available, API not yet frozen.

---

## Decision matrix

| Choice | Recommendation | Rationale |
|---|---|---|
| Semver doc location | `RELEASING.md` (new file) | Keeps AGENTS.md focused on agent guidance; RELEASING.md is the canonical place tooling authors look |
| `--version` placement | Cobra root `Version` field | It identifies the binary, not a vendor mode; root is correct. Cobra auto-adds `--version`/`-v` when `Version` is set |
| Signing in v1 | Skip; add cosign in v1.1+ | cosign-installer + keyless signing adds workflow complexity and a new prereq for local path; unsigned v0.x is acceptable; defer |
| Initial version | `v0.1.0` | Semver strictness (no breaking changes without major bump) kicks in at 1.0; `v0.x` lets us evolve the hook payload shape and CLI flags without ceremony |

---

## Implementation order (dep-ordered)

1. **`RELEASING.md`** — semver policy + cross-reference to `COMPATIBILITY.md`
2. **`main.go`** — add `var version = "dev"` + set `root.Version = version`
3. **`.goreleaser.yaml`** — cross-OS matrix, archives, checksums
4. **`.github/workflows/release.yml`** — tag-push trigger
5. **`Taskfile.yaml`** — add `release:local` task
6. **`COMPATIBILITY.md`** update — flip `--version` row from `✗ planned` → `✓ supported` (coordinate with the #8/#9 PR if it hasn't landed)

---

## File skeletons

### `RELEASING.md`

```markdown
# Releasing testagent

## Semver policy

testagent follows [Semantic Versioning](https://semver.org/) starting at v1.0.0.
Releases below v1.0.0 may change CLI flags and hook shapes without a major bump.

| Surface | Breaking | Additive | Non-breaking |
|---|---|---|---|
| CLI flags | Remove or rename a `✓ supported` flag | Add a new flag | Change default value of an internal flag |
| Hook payload fields | Remove/rename a field on any `✓ supported` event | Add an optional field | Change log verbosity |
| MCP protocol version | Drop a previously-advertised `protocolVersion` | Advertise an additional version | Internal MCP client refactor |
| Internal packages | N/A (not public API) | N/A | All changes |

See [COMPATIBILITY.md](COMPATIBILITY.md) for the current supported-surface matrix.
State transitions in that matrix drive the version bump decision:
- `✓ supported` → removed = **major** (or patch if pre-1.0)
- `✗ planned` → `✓ supported` = **minor**
- Bug fix within a supported surface = **patch**

## Cutting a release

### Prerequisites

- `goreleaser` installed (`brew install goreleaser` or `go install github.com/goreleaser/goreleaser/v2@latest`)
- `GH_TOKEN` env var set to a token with `repo` + `write:packages` scopes (`gh auth token`)
- `task ci` passes locally

### CI path (preferred)

Push a `vX.Y.Z` tag. GitHub Actions picks it up, runs goreleaser, and uploads
signed binaries to the GitHub release.

```sh
git tag v0.1.0
git push origin v0.1.0
```

### Local fallback

See **Manual-local runbook** below.
```

---

### `main.go` additions

In `/Users/paul.tyng/Documents/Ideate/2026-05-08-test-cli-agent/repos/testagent/main.go`, add after the imports block:

```go
// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
// Falls back to "dev" when running unbuilt (go run .).
var version = "dev"
```

In the `root` cobra.Command literal, add:

```go
Version: version,
```

Build flag: `go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" .`

---

### `.goreleaser.yaml`

```yaml
version: 2

before:
  hooks:
    - go mod tidy

builds:
  - id: testagent
    binary: testagent
    main: .
    ldflags:
      - -s -w -X main.version={{.Version}}
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64

archives:
  - id: default
    builds: [testagent]
    format_overrides:
      - goos: windows
        format: zip
    files:
      - LICENSE
      - README.md
    name_template: "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}"

checksum:
  name_template: "checksums.txt"
  algorithm: sha256

source:
  enabled: true
  name_template: "{{ .ProjectName }}_{{ .Version }}_source"

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^chore:"

release:
  github:
    owner: paultyng
    name: testagent
  draft: false
  prerelease: auto
```

---

### `.github/workflows/release.yml`

```yaml
name: Release

on:
  push:
    tags:
      - "v[0-9]*"

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Run goreleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

---

### `Taskfile.yaml` additions

```yaml
  release:local:
    desc: Cut a release from the local machine against the current tag. Requires GH_TOKEN and goreleaser.
    cmds:
      - |
        if ! command -v goreleaser &>/dev/null; then
          echo "ERROR: goreleaser not found. Install: brew install goreleaser"; exit 1
        fi
        if [ -z "$GH_TOKEN" ]; then
          echo "ERROR: GH_TOKEN not set. Run: export GH_TOKEN=$(gh auth token)"; exit 1
        fi
        goreleaser release --clean
```

---

## Manual-local runbook

1. Ensure prerequisites: `goreleaser` installed, `gh auth status` shows authenticated.
2. Export token: `export GH_TOKEN=$(gh auth token)`
3. Verify CI is green locally: `task ci`
4. Tag the commit: `git tag v0.1.0 && git push origin v0.1.0`
5. Run: `task release:local`

Artifacts land in `dist/`. The same archives and `checksums.txt` are uploaded to the GitHub release as the CI path produces. No signing in v0.x — archives include a plain `LICENSE` and `README.md`.

---

## Out of scope (v1)

- Homebrew tap / formula
- Scoop bucket
- winget submission
- Apple notarization (separate apple-id + password flow, requires macOS runner)
- cosign/Sigstore keyless signing (defer to v1.1)
- Linux package repos (deb/rpm)

---

## Risks / open questions

- **`COMPATIBILITY.md` coordination**: This plan assumes the #8/#9 PR lands first and seeds `COMPATIBILITY.md`. If it doesn't, skip the cross-reference in `RELEASING.md` and add it in a follow-up.
- **`root.Version` vs `-v` conflict**: Cobra's `Version` field adds both `--version` and `-v`. The root command already uses `-v` as the short form for `--verbose` (see `main.go` line `pf.BoolVarP(&rf.Verbose, "verbose", "v", ...)`). Cobra will reject registering `-v` twice. **Resolution**: either drop the `-v` short form from `--verbose` (use `--verbose` only) or set `root.Version` without relying on cobra's auto-added `-v` short flag — use `root.Flags().BoolP` manually for `--version` instead of the `Version` field, or just remove `-v` from verbose and let cobra own it for version. Recommend: drop `-v` from `--verbose`; `--verbose` is clear enough and verbose is rarely typed by hand.
- **`prerelease: auto`** in goreleaser treats any tag with a pre-release suffix (e.g. `v0.1.0-rc1`) as a GitHub pre-release. First production tag `v0.1.0` will publish as a full release.
