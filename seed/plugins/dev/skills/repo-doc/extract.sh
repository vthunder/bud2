#!/usr/bin/env bash
# extract.sh — collect repo context for repo-doc skill
# Usage: extract.sh <repo-path> <output-dir>
# Outputs files into <output-dir>/ for consumption by the synthesis step.

set -euo pipefail

REPO_PATH="${1:?Usage: extract.sh <repo-path> <output-dir>}"
OUT_DIR="${2:?Usage: extract.sh <repo-path> <output-dir>}"

# Normalize paths
REPO_PATH="$(cd "$REPO_PATH" && pwd)"
mkdir -p "$OUT_DIR"

echo "==> Extracting repo context"
echo "    repo:   $REPO_PATH"
echo "    output: $OUT_DIR"
echo ""

# --- repomix availability check ---
REPOMIX_AVAILABLE=0
REPOMIX_VERSION="unavailable"

if command -v repomix &>/dev/null; then
  REPOMIX_AVAILABLE=1
  REPOMIX_VERSION="$(repomix --version 2>/dev/null || echo 'unknown')"
  echo "==> repomix $REPOMIX_VERSION found"
else
  echo "!!! repomix not found."
  echo "    Install: npm install -g repomix"
  echo "    Falling back to find-based extraction (limited quality)."
  echo ""
  REPOMIX_AVAILABLE=0
fi

# --- repomix extraction ---
if [ "$REPOMIX_AVAILABLE" -eq 1 ]; then
  # Common generated/vendor patterns to exclude from analysis
  IGNORE_PATTERNS="*_pb2.py,*.pb.go,**/migrations/**,**/vendor/**,**/.venv/**,**/node_modules/**,**/dist/**,**/build/**,**/__pycache__/**"

  echo "==> Running repomix compressed (structure + signatures)..."
  repomix \
    --compress \
    --style markdown \
    --ignore "$IGNORE_PATTERNS" \
    --output "$OUT_DIR/compressed.md" \
    "$REPO_PATH" \
    2>/dev/null && echo "    wrote compressed.md" \
    || echo "    WARNING: repomix compressed failed, skipping"

  echo "==> Running repomix tree (file tree only)..."
  repomix \
    --no-files \
    --style markdown \
    --ignore "$IGNORE_PATTERNS" \
    --output "$OUT_DIR/tree.md" \
    "$REPO_PATH" \
    2>/dev/null && echo "    wrote tree.md" \
    || echo "    WARNING: repomix tree failed, skipping"
else
  # Fallback: collect file list via find
  echo "==> Fallback: collecting file list..."
  {
    echo "# File List (fallback — repomix unavailable)"
    echo ""
    echo "> NOTE: repomix was not available. This overview was generated from file listing"
    echo "> and manifest files only. Quality will be lower. Install repomix and re-run."
    echo ""
    find "$REPO_PATH" \
      \( -name "*.go" -o -name "*.ts" -o -name "*.tsx" -o -name "*.js" \
         -o -name "*.jsx" -o -name "*.php" -o -name "*.py" -o -name "*.rb" \
         -o -name "*.rs" -o -name "*.java" -o -name "*.kt" \) \
      -not -path "*/vendor/*" \
      -not -path "*/node_modules/*" \
      -not -path "*/.git/*" \
      -not -path "*/dist/*" \
      -not -path "*/build/*" \
      | sort \
      | head -100
  } > "$OUT_DIR/tree.md"
  echo "    wrote tree.md (fallback)"
  touch "$OUT_DIR/compressed.md"
  echo "    wrote compressed.md (empty — repomix unavailable)"
fi

# --- README ---
echo ""
echo "==> Looking for README..."
README_FOUND=0
for name in README.md README.txt README.rst README; do
  if [ -f "$REPO_PATH/$name" ]; then
    cp "$REPO_PATH/$name" "$OUT_DIR/readme.md"
    echo "    found $name"
    README_FOUND=1
    break
  fi
done
if [ "$README_FOUND" -eq 0 ]; then
  echo "    no README found"
  echo "# (no README found)" > "$OUT_DIR/readme.md"
fi

# --- Package manifest ---
echo ""
echo "==> Looking for package manifest..."
MANIFEST_FOUND=0

if [ -f "$REPO_PATH/package.json" ]; then
  echo "# package.json" > "$OUT_DIR/manifest.md"
  echo '```json' >> "$OUT_DIR/manifest.md"
  cat "$REPO_PATH/package.json" >> "$OUT_DIR/manifest.md"
  echo '```' >> "$OUT_DIR/manifest.md"
  echo "    found package.json"
  MANIFEST_FOUND=1
fi

if [ -f "$REPO_PATH/go.mod" ]; then
  {
    echo "# go.mod"
    echo '```'
    cat "$REPO_PATH/go.mod"
    echo '```'
  } >> "$OUT_DIR/manifest.md"
  echo "    found go.mod"
  MANIFEST_FOUND=1
fi

if [ -f "$REPO_PATH/composer.json" ]; then
  {
    echo "# composer.json"
    echo '```json'
    cat "$REPO_PATH/composer.json"
    echo '```'
  } >> "$OUT_DIR/manifest.md"
  echo "    found composer.json"
  MANIFEST_FOUND=1
fi

if [ -f "$REPO_PATH/Cargo.toml" ]; then
  {
    echo "# Cargo.toml"
    echo '```toml'
    cat "$REPO_PATH/Cargo.toml"
    echo '```'
  } >> "$OUT_DIR/manifest.md"
  echo "    found Cargo.toml"
  MANIFEST_FOUND=1
fi

if [ "$MANIFEST_FOUND" -eq 0 ]; then
  echo "    no manifest found"
  echo "# (no manifest found)" > "$OUT_DIR/manifest.md"
fi

# --- Per-module scoring data ---
echo ""
echo "==> Collecting per-module scoring data..."

# Detect primary language for file extensions
HAS_GO=0
HAS_TS=0
HAS_PY=0
[ -f "$REPO_PATH/go.mod" ] && HAS_GO=1
[ -f "$REPO_PATH/package.json" ] || [ -f "$REPO_PATH/tsconfig.json" ] && HAS_TS=1
[ -f "$REPO_PATH/setup.py" ] || [ -f "$REPO_PATH/pyproject.toml" ] || [ -f "$REPO_PATH/requirements.txt" ] && HAS_PY=1

# Build file extension glob for the detected language(s)
if [ "$HAS_GO" -eq 1 ]; then
  SRC_EXT="*.go"
elif [ "$HAS_TS" -eq 1 ]; then
  SRC_EXT="*.ts *.tsx *.js *.jsx"
elif [ "$HAS_PY" -eq 1 ]; then
  SRC_EXT="*.py"
else
  SRC_EXT="*.go *.ts *.tsx *.js *.py *.rb *.rs *.java *.kt"
fi

# For Go: extract module prefix from go.mod (e.g. "github.com/foo/bud2")
MODULE_PREFIX=""
if [ "$HAS_GO" -eq 1 ] && [ -f "$REPO_PATH/go.mod" ]; then
  MODULE_PREFIX=$(awk '/^module /{print $2; exit}' "$REPO_PATH/go.mod")
fi

# Collect top-level source directories (skip common non-code dirs)
SKIP_DIRS="vendor node_modules .git dist build __pycache__ .venv target .cache coverage .nyc_output"

{
  echo "# Per-Module Scoring Data"
  echo "# Repo: $REPO_PATH"
  echo "# Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "# Module prefix (Go): $MODULE_PREFIX"
  echo ""
  echo "| Module | Files | LoC | Commits 90d | Fix-Commits 90d | Centrality (import refs) |"
  echo "|--------|-------|-----|-------------|-----------------|--------------------------|"

  # Find candidate directories (depth 1–2)
  find "$REPO_PATH" -mindepth 1 -maxdepth 2 -type d | sort | while read -r dir; do
    basename_dir="$(basename "$dir")"

    # Skip hidden dirs and known non-code dirs
    skip=0
    for s in $SKIP_DIRS; do
      [ "$basename_dir" = "$s" ] && { skip=1; break; } || true
    done
    echo "$basename_dir" | grep -q '^\.' && skip=1 || true
    [ "$skip" -eq 1 ] && continue || true

    # Relative path from repo root
    rel_path="${dir#$REPO_PATH/}"

    # File count (src files only)
    file_count=0
    for ext in $SRC_EXT; do
      c=$(find "$dir" -maxdepth 3 -name "$ext" -not -path "*/vendor/*" -not -path "*_test.go" 2>/dev/null | wc -l | tr -d ' ')
      file_count=$((file_count + c))
    done

    # Skip dirs with no source files
    [ "$file_count" -eq 0 ] && continue || true

    # LoC estimate
    loc=0
    for ext in $SRC_EXT; do
      l=$(find "$dir" -maxdepth 3 -name "$ext" -not -path "*/vendor/*" 2>/dev/null \
        | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
      l=${l:-0}
      loc=$((loc + l))
    done

    # Git churn (90 days, non-test files)
    commits_90d=$(git -C "$REPO_PATH" log --since="90 days ago" --oneline -- "$rel_path" 2>/dev/null | wc -l | tr -d ' ')

    # Bug/fix commits (90 days)
    fix_commits_90d=$(git -C "$REPO_PATH" log --since="90 days ago" --oneline --grep='\(fix\|bug\|Fix\|Bug\|patch\)' -- "$rel_path" 2>/dev/null | wc -l | tr -d ' ')

    # Centrality: count import references in compressed.md
    # Use '; true' to suppress exit-code 1 from grep (no matches) under set -euo pipefail
    centrality=0
    if [ -f "$OUT_DIR/compressed.md" ]; then
      if [ "$HAS_GO" -eq 1 ] && [ -n "$MODULE_PREFIX" ]; then
        centrality=$(grep -o "\"${MODULE_PREFIX}/${rel_path}" "$OUT_DIR/compressed.md" 2>/dev/null | wc -l | tr -d ' '; true)
      else
        centrality=$(grep -o "\"${rel_path}" "$OUT_DIR/compressed.md" 2>/dev/null | wc -l | tr -d ' '; true)
      fi
    fi

    echo "| \`$rel_path\` | $file_count | $loc | $commits_90d | $fix_commits_90d | $centrality |"
  done
} > "$OUT_DIR/scoring-data.md"

echo "    wrote scoring-data.md"

# --- Summary ---
echo ""
echo "==> Summary"
{
  echo "repomix_version=$REPOMIX_VERSION"
  echo "repomix_available=$REPOMIX_AVAILABLE"
  echo ""
  echo "Files collected:"
  for f in compressed.md tree.md readme.md manifest.md scoring-data.md; do
    fpath="$OUT_DIR/$f"
    if [ -f "$fpath" ]; then
      size=$(wc -c < "$fpath" | tr -d ' ')
      lines=$(wc -l < "$fpath" | tr -d ' ')
      # Rough token estimate: ~4 chars per token
      tokens=$(( size / 4 ))
      printf "  %-20s  %6d bytes  %5d lines  ~%d tokens\n" "$f" "$size" "$lines" "$tokens"
    fi
  done
} | tee "$OUT_DIR/summary.txt"

echo ""
echo "==> Done. Output in $OUT_DIR"
