#!/usr/bin/env bash
# upstream-drift.sh — detect drift between testagent's upstream config corpus
# and vendor docs; create a draft PR if net-new passing fixtures are found.
#
# Usage: upstream-drift.sh <vendor>
#   vendor: claude | codex | cursor
#
# Required tools: bash, curl, jq, awk, gh, go, git
# Required env:   GH_TOKEN (set by the calling workflow)

set -euo pipefail

VENDOR="${1:-}"
if [[ -z "$VENDOR" ]]; then
  echo "Usage: $0 <vendor>" >&2
  exit 1
fi

case "$VENDOR" in
  claude|codex|cursor) ;;
  *)
    echo "Unknown vendor: $VENDOR (must be claude, codex, or cursor)" >&2
    exit 1
    ;;
esac

REPO_ROOT="$(git rev-parse --show-toplevel)"
CORPUS_DIR="$REPO_ROOT/testdata/upstream-examples/$VENDOR"
TESTAGENT_BIN="${TESTAGENT_BIN:-/tmp/testagent}"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$CORPUS_DIR"

# ---------------------------------------------------------------------------
# Step 1: Fetch source content
# ---------------------------------------------------------------------------

fetch_claude_pages() {
  local llms_txt
  llms_txt="$(curl -sL https://code.claude.com/llms.txt)"
  # Extract URLs containing "hooks" or "mcp"
  grep -Ei 'hooks|mcp' <<< "$llms_txt" | grep -oE 'https?://[^ ]+' | sort -u
}

fetch_cursor_pages() {
  local llms_txt
  llms_txt="$(curl -sL https://cursor.com/llms.txt)"
  grep -Ei 'hooks|mcp' <<< "$llms_txt" | grep -oE 'https?://[^ ]+' | sort -u
}

fetch_codex_readme() {
  gh api repos/openai/codex/contents/README.md -H 'Accept: application/vnd.github.raw'
}

# ---------------------------------------------------------------------------
# Step 2: Extract fenced code blocks (awk)
# ---------------------------------------------------------------------------

# extract_blocks <lang> <input-file> <output-dir>
# Writes one file per block: block_<n>.<ext>
extract_blocks() {
  local lang="$1"
  local input="$2"
  local outdir="$3"
  local ext
  case "$lang" in
    json) ext="json" ;;
    toml) ext="toml" ;;
    *) ext="txt" ;;
  esac

  # Closer matches `^``` <optional trailing whitespace> $` rather than
  # `^```` (anywhere) so a 4-backtick wrapper inside a 3-backtick block
  # doesn't false-close the capture.
  awk -v lang="$lang" -v outdir="$outdir" -v ext="$ext" '
    BEGIN { in_block=0; block_n=0; buf="" }
    /^```'"$lang"'[[:space:]]*$/ {
      in_block=1; buf=""; next
    }
    in_block && /^```[[:space:]]*$/ {
      in_block=0
      if (length(buf) > 0) {
        block_n++
        fname = outdir "/block_" block_n "." ext
        printf "%s", buf > fname
        close(fname)
      }
      buf=""
      next
    }
    in_block { buf = buf $0 "\n" }
  ' "$input"
}

# ---------------------------------------------------------------------------
# Step 3: Compute SHA256 of a file (portable)
# ---------------------------------------------------------------------------

sha256_file() {
  if command -v sha256sum &>/dev/null; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# ---------------------------------------------------------------------------
# Step 4: Compare candidates against corpus
# ---------------------------------------------------------------------------

# Build a map of SHA256 -> filename for the corpus
declare -A CORPUS_SHAS
shopt -s nullglob
for f in "$CORPUS_DIR"/*.json "$CORPUS_DIR"/*.toml; do
  [[ -f "$f" ]] || continue
  sha="$(sha256_file "$f")"
  CORPUS_SHAS["$sha"]="$f"
done
shopt -u nullglob

ADDED_FILES=()
SKIPPED_COUNT=0
ADDED_SOURCES=()

# detect_slot <candidate> <vendor>
# Pick the file slot a doc example belongs in based on its top-level
# JSON keys. The slot determines:
#   - where in the sandbox the candidate gets dropped before
#     `validate --strict` runs
#   - the slug prefix of the vendored fixture, which the Phase 2
#     parameterized tests use to dispatch on re-validation
# Trial-and-error slot routing pollutes the corpus when a lax slot
# (e.g. cursor's parse-only mcp.json validation) accepts content that
# really belongs in another slot; pick from the shape, not from which
# slot happens to not error.
detect_slot() {
  local candidate="$1"
  local vendor="$2"
  case "$vendor" in
    claude)
      # Combined settings.json (real Claude Code accepts hooks +
      # mcpServers in one file) → settings. Otherwise route by which
      # of the two top-level keys is present.
      if jq -e 'has("mcpServers") and (has("hooks") or has("permissions"))' "$candidate" >/dev/null 2>&1; then
        echo "settings"; return 0
      fi
      if jq -e 'has("mcpServers")' "$candidate" >/dev/null 2>&1; then
        echo "mcp"; return 0
      fi
      if jq -e 'has("hooks") or has("permissions")' "$candidate" >/dev/null 2>&1; then
        echo "settings"; return 0
      fi
      return 1
      ;;
    codex)
      # codex is single-file; no detection needed.
      echo "config"; return 0
      ;;
    cursor)
      if jq -e 'has("mcpServers")' "$candidate" >/dev/null 2>&1; then
        echo "mcp"; return 0
      fi
      if jq -e 'has("hooks")' "$candidate" >/dev/null 2>&1; then
        echo "hooks"; return 0
      fi
      return 1
      ;;
  esac
  return 1
}

# try_validate <candidate> <vendor>
# Detect the slot the candidate belongs in, drop it at the vendor-
# expected path, run `validate --strict`. Echo the slot on success.
# A candidate whose shape doesn't match any known slot is skipped
# (counted toward SKIPPED_COUNT by the caller).
try_validate() {
  local candidate="$1"
  local vendor="$2"
  local slot
  slot="$(detect_slot "$candidate" "$vendor")" || return 1

  local sandbox
  sandbox="$(mktemp -d "$WORK_DIR/sandbox_XXXXXX")"

  case "$vendor" in
    claude)
      case "$slot" in
        mcp)
          "$TESTAGENT_BIN" claude validate --strict --mcp-config "$candidate" &>/dev/null || return 1
          ;;
        settings)
          "$TESTAGENT_BIN" claude validate --strict --settings "$candidate" &>/dev/null || return 1
          ;;
      esac
      ;;
    codex)
      cp "$candidate" "$sandbox/config.toml"
      CODEX_HOME="$sandbox" "$TESTAGENT_BIN" codex validate --strict &>/dev/null || return 1
      ;;
    cursor)
      mkdir -p "$sandbox/.cursor"
      cp "$candidate" "$sandbox/.cursor/${slot}.json"
      "$TESTAGENT_BIN" cursor validate --strict --workspace "$sandbox" &>/dev/null || return 1
      ;;
  esac
  echo "$slot"
}

process_candidate() {
  local candidate="$1"
  local source_url="${2:-unknown}"
  local lang="${3:-json}"

  # Skip empty or non-relevant blocks (must contain hooks/mcp keywords)
  if ! grep -qiE 'hooks|mcp|hook_type|event_type|\[hooks\.' "$candidate" 2>/dev/null; then
    return
  fi

  local sha
  sha="$(sha256_file "$candidate")"

  # Already vendored verbatim?
  if [[ -n "${CORPUS_SHAS[$sha]:-}" ]]; then
    return
  fi

  local ext="$lang"
  local slot
  if slot="$(try_validate "$candidate" "$VENDOR")"; then
    # Passing — propose for corpus. Slug embeds the accepting slot so
    # the Phase 2 parameterized tests route the fixture correctly on
    # re-validation.
    local slug
    slug="${slot}-upstream-$(date +%Y%m%d)-$(echo "$sha" | cut -c1-8)"
    local dest="$CORPUS_DIR/${slug}.${ext}"
    cp "$candidate" "$dest"

    # Write .source sibling
    cat > "${dest%."${ext}"}.source" <<EOF
url: $source_url
verified: $(date +%Y-%m-%d)
EOF

    ADDED_FILES+=("$dest")
    ADDED_SOURCES+=("$source_url")
  else
    SKIPPED_COUNT=$((SKIPPED_COUNT + 1))
  fi
}

# ---------------------------------------------------------------------------
# Step 5: Per-vendor fetch + extract + process
# ---------------------------------------------------------------------------

LANG="json"
case "$VENDOR" in
  codex) LANG="toml" ;;
esac

case "$VENDOR" in
  claude)
    while IFS= read -r page_url; do
      [[ -z "$page_url" ]] && continue
      page_content="$(curl -sL "$page_url" || true)"
      if [[ -z "$page_content" ]]; then
        continue
      fi
      page_file="$(mktemp "$WORK_DIR/page_XXXXXX.md")"
      printf '%s' "$page_content" > "$page_file"
      block_dir="$(mktemp -d "$WORK_DIR/blocks_XXXXXX")"
      extract_blocks "json" "$page_file" "$block_dir"
      for block in "$block_dir"/block_*.json; do
        [[ -f "$block" ]] || continue
        process_candidate "$block" "$page_url" "json"
      done
    done < <(fetch_claude_pages)
    ;;

  cursor)
    while IFS= read -r page_url; do
      [[ -z "$page_url" ]] && continue
      page_content="$(curl -sL "$page_url" || true)"
      if [[ -z "$page_content" ]]; then
        continue
      fi
      page_file="$(mktemp "$WORK_DIR/page_XXXXXX.md")"
      printf '%s' "$page_content" > "$page_file"
      block_dir="$(mktemp -d "$WORK_DIR/blocks_XXXXXX")"
      extract_blocks "json" "$page_file" "$block_dir"
      for block in "$block_dir"/block_*.json; do
        [[ -f "$block" ]] || continue
        process_candidate "$block" "$page_url" "json"
      done
    done < <(fetch_cursor_pages)
    ;;

  codex)
    readme_file="$(mktemp "$WORK_DIR/readme_XXXXXX.md")"
    fetch_codex_readme > "$readme_file"
    block_dir="$(mktemp -d "$WORK_DIR/blocks_XXXXXX")"
    extract_blocks "toml" "$readme_file" "$block_dir"
    for block in "$block_dir"/block_*.toml; do
      [[ -f "$block" ]] || continue
      process_candidate "$block" "https://github.com/openai/codex/blob/main/README.md" "toml"
    done
    ;;
esac

# ---------------------------------------------------------------------------
# Step 6: De-dupe guard
# ---------------------------------------------------------------------------

if [[ ${#ADDED_FILES[@]} -eq 0 ]]; then
  echo "No net-new passing fixtures found for $VENDOR. Corpus is up to date."
  exit 0
fi

# GitHub search treats `(` and `)` as query delimiters, so the title
# literal must not be passed in the --search expression. List open PRs
# and exact-match the title via jq instead.
EXISTING_PR="$(gh pr list --state open --json number,url,title --jq \
  '.[] | select(.title == "chore(testdata): upstream-config drift detected ('"$VENDOR"')") | .url' \
  2>/dev/null | head -n 1 || true)"
if [[ -n "$EXISTING_PR" ]]; then
  echo "Drift PR already open for $VENDOR: $EXISTING_PR — skipping creation."
  exit 0
fi

# ---------------------------------------------------------------------------
# Step 7: Build PR body
# ---------------------------------------------------------------------------

PR_BODY_FILE="$(mktemp "$WORK_DIR/pr-body_XXXXXX.md")"

cat > "$PR_BODY_FILE" <<PRBODY
## Upstream config drift detected — $VENDOR

Net-new examples found in upstream docs that pass \`testagent $VENDOR validate --strict\`. Adding to the corpus.

### Added
PRBODY

for i in "${!ADDED_FILES[@]}"; do
  rel_path="${ADDED_FILES[$i]#$REPO_ROOT/}"
  src="${ADDED_SOURCES[$i]:-unknown}"
  echo "- \`$rel_path\` (from $src)" >> "$PR_BODY_FILE"
done

cat >> "$PR_BODY_FILE" <<PRBODY

### Drifted (existing fixture diverges from upstream)

None detected in this run.

### Skipped — fail --strict

- $SKIPPED_COUNT examples failed \`testagent $VENDOR validate --strict\`; tracked separately in \`update-compatibility\`.

## How to evaluate

Each Added fixture passed \`testagent $VENDOR validate --strict\` in the
workflow. That's necessary but not sufficient — **runtime behavior of
the actual vendor binary (\`claude\` / \`codex\` / \`cursor\`) is the
canonical source of truth, not the docs page**. testagent's job is to
match what the real vendor accepts, even when testagent's own internal
runtime doesn't model every field.

Triage rules:

1. **Added**: spot-check each fixture matches what the real vendor
   binary would accept. If our validator accepts a fixture but the
   doc example looks stale or wrong (typo, deprecated key), drop it
   from this PR and file an upstream doc bug — **don't widen our
   validator to match a wrong doc**.
2. **Skipped — fail --strict**: doc examples we couldn't accept.
   Sometimes the doc is right and we're missing schema coverage
   (→ extend the allowlist via \`update-compatibility\`). Sometimes
   the doc is wrong (→ leave it skipped).
3. **Drifted**: existing fixture diverges from upstream. If the real
   vendor actually broke compat, treat it as a vendor break and
   update the validator. If only the docs drifted, skip.

When uncertain, run the real \`$VENDOR\` binary against the fixture in
a sandbox before merging.

🤖 Generated by \`.github/workflows/upstream-drift.yml\`
PRBODY

# ---------------------------------------------------------------------------
# Step 8: Create branch + commit + PR
# ---------------------------------------------------------------------------

DATE="$(date +%Y%m%d)"
BRANCH="chore/upstream-drift-${VENDOR}-${DATE}"

# GitHub Actions runners have no default git identity; without these
# two configs the commit below fails with "empty ident name not
# allowed". Using the conventional github-actions[bot] identity.
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git config user.name "github-actions[bot]"

git checkout -b "$BRANCH"
git add "$CORPUS_DIR"
git commit -m "chore(testdata): upstream-config drift detected ($VENDOR)"

# If a remote branch with this name exists from a prior closed/abandoned
# attempt on the same day, drop it before pushing. The dedupe guard
# above already short-circuits the open-PR case, so reaching this point
# means any stale ref is from a closed PR and safe to discard.
git push --delete origin "$BRANCH" 2>/dev/null || true
git push origin "$BRANCH"

gh pr create \
  --draft \
  --title "chore(testdata): upstream-config drift detected ($VENDOR)" \
  --body "$(cat "$PR_BODY_FILE")"

echo "Draft PR created for $VENDOR drift."
