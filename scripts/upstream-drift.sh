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

  awk -v lang="$lang" -v outdir="$outdir" -v ext="$ext" '
    BEGIN { in_block=0; block_n=0; buf="" }
    /^```'"$lang"'/ {
      in_block=1; buf=""; next
    }
    in_block && /^```/ {
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
for f in "$CORPUS_DIR"/*.json "$CORPUS_DIR"/*.toml 2>/dev/null; do
  [[ -f "$f" ]] || continue
  sha="$(sha256_file "$f")"
  CORPUS_SHAS["$sha"]="$f"
done

ADDED_FILES=()
DRIFTED_FILES=()
SKIPPED_COUNT=0
ADDED_SOURCES=()

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

  # Run validator
  local validate_output
  local ext="$lang"
  local tmp_candidate
  tmp_candidate="$(mktemp "$WORK_DIR/candidate_XXXXXX.$ext")"
  cp "$candidate" "$tmp_candidate"

  if "$TESTAGENT_BIN" "$VENDOR" validate --strict < "$tmp_candidate" &>/dev/null; then
    # Passing — propose for corpus
    # Generate a slug from content hash (first 8 chars) to avoid collisions
    local slug
    slug="upstream-$(date +%Y%m%d)-$(echo "$sha" | cut -c1-8)"
    local dest="$CORPUS_DIR/${slug}.${ext}"
    cp "$tmp_candidate" "$dest"

    # Write .source sibling
    cat > "${dest%.${ext}}.source" <<EOF
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

EXISTING_PR="$(gh pr list --search "in:title \"upstream-config drift detected ($VENDOR)\" state:open" --json number,url --jq '.[0].url' 2>/dev/null || true)"
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

🤖 Generated by \`.github/workflows/upstream-drift.yml\`
PRBODY

# ---------------------------------------------------------------------------
# Step 8: Create branch + commit + PR
# ---------------------------------------------------------------------------

DATE="$(date +%Y%m%d)"
BRANCH="chore/upstream-drift-${VENDOR}-${DATE}"

git checkout -b "$BRANCH"
git add "$CORPUS_DIR"
git commit -m "chore(testdata): upstream-config drift detected ($VENDOR)"

git push origin "$BRANCH"

gh pr create \
  --draft \
  --title "chore(testdata): upstream-config drift detected ($VENDOR)" \
  --body "$(cat "$PR_BODY_FILE")"

echo "Draft PR created for $VENDOR drift."
