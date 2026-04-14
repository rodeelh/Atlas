#!/usr/bin/env bash
# count-loc.sh — Count lines of code for Project Atlas
# Usage: ./scripts/count-loc.sh [--detail]

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DETAIL="${1:-}"

sep() { printf '%s\n' "-------------------------------------------"; }

loc() {
  find "$@" -print0 | xargs -0 wc -l 2>/dev/null | tail -1 | awk '{print $1}'
}

top_files() {
  local root_prefix="$1"; shift
  # Count each file individually, sort, show top 20
  while IFS= read -r -d '' f; do
    wc -l < "$f" | tr -d ' ' | paste - <(echo "$f")
  done < <(find "$@" -print0) \
    | sort -rn \
    | head -20 \
    | while IFS=$'\t' read -r count path; do
        rel="${path#"$root_prefix"/}"
        printf "  %-55s %6s\n" "$rel" "$count"
      done
}

echo ""
echo "  Project Atlas — Lines of Code"
sep

GO=$(loc  "$ROOT/atlas-runtime" -name "*.go" ! -path "*/vendor/*")
WEB=$(loc "$ROOT/atlas-web/src" \( -name "*.ts" -o -name "*.tsx" -o -name "*.css" \))
SCR=$(loc "$ROOT/scripts"       \( -name "*.sh" -o -name "*.py" \))
TOTAL=$((${GO:-0} + ${WEB:-0} + ${SCR:-0}))

printf "%-30s %6s lines\n" "Go runtime (atlas-runtime)" "${GO:-0}"
printf "%-30s %6s lines\n" "Web UI (atlas-web)"         "${WEB:-0}"
printf "%-30s %6s lines\n" "Scripts"                    "${SCR:-0}"
sep
printf "%-30s %6s lines\n" "TOTAL"                      "$TOTAL"
echo ""

if [[ "$DETAIL" == "--detail" ]]; then
  echo "  Top Go files:"
  sep
  top_files "$ROOT/atlas-runtime" "$ROOT/atlas-runtime" -name "*.go" ! -path "*/vendor/*"
  echo ""

  echo "  Top Web files:"
  sep
  top_files "$ROOT/atlas-web/src" "$ROOT/atlas-web/src" \( -name "*.ts" -o -name "*.tsx" \)
  echo ""
fi
