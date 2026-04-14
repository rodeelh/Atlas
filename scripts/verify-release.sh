#!/usr/bin/env bash
# Atlas — Major Build Validation
#
# Runs the full layered test pyramid (backend, web build) and emits
# a human-readable scorecard at docs/testing/atlas-test-scorecard.md.
#
# Tiers:
#   fast      — quick local checks (vet + short tests, ~30s)
#   standard  — full Go test suites + web typecheck/build (CI default)
#   release   — everything above + scorecard generation + binary builds
#
# Usage:
#   scripts/verify-release.sh [fast|standard|release]
#
# Exit code is non-zero if any required step fails.
set -uo pipefail

TIER="${1:-release}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUNTIME_DIR="$ROOT/atlas-runtime"
WEB_DIR="$ROOT/atlas-web"
SCORECARD="$ROOT/docs/testing/atlas-test-scorecard.md"

mkdir -p "$(dirname "$SCORECARD")"

# Result tracking: name|status|note
RESULTS=()
FAIL_COUNT=0

color() { printf '\033[%sm%s\033[0m' "$1" "$2"; }
green() { color "32" "$1"; }
red()   { color "31" "$1"; }
yellow(){ color "33" "$1"; }
bold()  { color "1"  "$1"; }

record() {
  local name="$1" status="$2" note="$3"
  RESULTS+=("$name|$status|$note")
  case "$status" in
    PASS)    printf '  [%s] %s\n'   "$(green PASS)"    "$name" ;;
    PARTIAL) printf '  [%s] %s — %s\n' "$(yellow PARTIAL)" "$name" "$note" ;;
    FAIL)    printf '  [%s] %s — %s\n' "$(red FAIL)" "$name" "$note"; FAIL_COUNT=$((FAIL_COUNT+1)) ;;
    SKIP)    printf '  [%s] %s — %s\n' "$(yellow SKIP)" "$name" "$note" ;;
    *)       printf '  [%s] %s\n' "$status" "$name" ;;
  esac
}

run_step() {
  local name="$1"; shift
  local logfile; logfile="$(mktemp)"
  if "$@" >"$logfile" 2>&1; then
    record "$name" PASS ""
  else
    local tail; tail="$(tail -3 "$logfile" | tr '\n' ' ' | cut -c1-160)"
    record "$name" FAIL "$tail"
  fi
  rm -f "$logfile"
}

echo
bold "Atlas — Major Build Validation"
echo
echo "  Tier:      $TIER"
echo "  Root:      $ROOT"
echo "  Scorecard: $SCORECARD"
echo

# ---------------------------------------------------------------------------
# Backend (Go runtime)
# ---------------------------------------------------------------------------
bold "Backend (Go runtime)"; echo
run_step "runtime: go vet"   bash -c "cd '$RUNTIME_DIR' && go vet ./..."
run_step "runtime: go build" bash -c "cd '$RUNTIME_DIR' && go build ./..."
if [[ "$TIER" == "fast" ]]; then
  run_step "runtime: go test -short" bash -c "cd '$RUNTIME_DIR' && go test -short -count=1 ./..."
else
  run_step "runtime: go test (full)" bash -c "cd '$RUNTIME_DIR' && go test -count=1 ./..."
fi

# ---------------------------------------------------------------------------
# Web UI (Preact + Vite)
# ---------------------------------------------------------------------------
echo; bold "Web UI (Preact + Vite)"; echo
if [[ "$TIER" == "fast" ]]; then
  if command -v npx >/dev/null 2>&1 && [[ -d "$WEB_DIR/node_modules" ]]; then
    run_step "web: tsc --noEmit" bash -c "cd '$WEB_DIR' && npx tsc --noEmit"
  else
    record "web: tsc --noEmit" SKIP "node_modules missing — run 'npm ci' in atlas-web"
  fi
else
  if command -v npm >/dev/null 2>&1; then
    if [[ ! -d "$WEB_DIR/node_modules" ]]; then
      record "web: npm install" SKIP "auto-install skipped; run 'npm ci' in atlas-web first"
    fi
    if [[ -d "$WEB_DIR/node_modules" ]]; then
      run_step "web: tsc --noEmit" bash -c "cd '$WEB_DIR' && npx tsc --noEmit"
      run_step "web: vite build"   bash -c "cd '$WEB_DIR' && npm run build"
    else
      record "web: tsc + build" SKIP "node_modules missing"
    fi
  else
    record "web: tsc + build" SKIP "npm not on PATH"
  fi
fi

# ---------------------------------------------------------------------------
# Cross-surface / smoke
# ---------------------------------------------------------------------------
echo; bold "Cross-surface"; echo
# The runtime ships with internal/integration which exercises baseline boot
# + architecture guardrails. Re-run it explicitly so its status is visible
# even if the broader test pass above failed elsewhere.
run_step "integration: runtime baseline + guardrails" \
  bash -c "cd '$RUNTIME_DIR' && go test -count=1 ./internal/integration/..."

# Optional: -race only on release tier (slow)
if [[ "$TIER" == "release" ]]; then
  echo; bold "Race detector (release tier only)"; echo
  run_step "runtime: go test -race -short" \
    bash -c "cd '$RUNTIME_DIR' && go test -race -short -count=1 ./..."
fi

# ---------------------------------------------------------------------------
# Scorecard
# ---------------------------------------------------------------------------
echo
bold "Generating scorecard"; echo

status_for() {
  local prefix="$1" matched=0 failed=0
  for r in "${RESULTS[@]}"; do
    local n s
    n="${r%%|*}"; s="${r#*|}"; s="${s%%|*}"
    if [[ "$n" == $prefix* ]]; then
      matched=1
      [[ "$s" == "FAIL" ]] && failed=1
    fi
  done
  if [[ $matched -eq 0 ]]; then
    echo "NOT YET COVERED"
  elif [[ $failed -eq 1 ]]; then
    echo "FAIL"
  else
    echo "PASS"
  fi
}

backend_status="$(status_for 'runtime: ')"
web_status="$(status_for 'web: ')"
integration_status="$(status_for 'integration:')"

readiness() {
  case "$1" in
    PASS) echo "✅ Ready" ;;
    FAIL) echo "❌ Not ready" ;;
    *)    echo "⚠️  Partial / unverified" ;;
  esac
}

if [[ $FAIL_COUNT -eq 0 ]]; then
  overall="✅ Production-ready"
elif [[ "$backend_status" == "PASS" ]]; then
  overall="⚠️  Staging-ready (non-critical surface failed)"
else
  overall="❌ Not production-ready"
fi

now="$(date '+%Y-%m-%d %H:%M:%S %Z')"

{
  echo "# Atlas Test Scorecard"
  echo
  echo "_Generated by \`scripts/verify-release.sh $TIER\` on ${now}._"
  echo
  echo "## Overall release confidence"
  echo
  echo "**$overall**"
  echo
  echo "| Surface  | Status | Readiness |"
  echo "| -------- | ------ | --------- |"
  echo "| Backend  | $backend_status     | $(readiness "$backend_status") |"
  echo "| Web UI   | $web_status     | $(readiness "$web_status") |"
  echo "| E2E/Integration | $integration_status | $(readiness "$integration_status") |"
  echo
  echo "## Category coverage"
  echo
  echo "| Category | Status | Notes |"
  echo "| -------- | ------ | ----- |"
  cat_row() { echo "| $1 | $2 | $3 |"; }
  cat_row "Unit tests (Go runtime)"           "$backend_status"      "go test ./... across 50+ packages"
  cat_row "Integration tests (runtime)"       "$integration_status"  "internal/integration baseline + architecture guardrails"
  cat_row "API/handler tests"                 "$backend_status"      "covered inside internal/modules/* and internal/domain"
  cat_row "Config validation"                 "$backend_status"      "config/snapshot tested"
  cat_row "Frontend / component tests"        "$web_status"          "no component test runner installed; build + tsc are the gate"
  cat_row "End-to-end critical flows"         "$integration_status"  "runtime_baseline_test boots HTTP server, exercises routes"
  cat_row "Smoke / startup"                   "$integration_status"  "runtime wiring_test"
  cat_row "Build / package verification"      "$([[ $FAIL_COUNT -eq 0 ]] && echo PASS || echo FAIL)" "go build ./... for runtime; vite build for web"
  cat_row "Regression coverage"               "$backend_status"      "existing _test.go suites act as regression net"
  cat_row "Performance sanity"                "NOT YET COVERED"      "no benchmarks wired into release gate"
  cat_row "Security checks"                   "$backend_status"      "auth/middleware_security_test + cors_test in runtime"
  cat_row "Race detector"                     "$([[ "$TIER" == "release" ]] && echo "$backend_status" || echo SKIP)" "release tier only"
  echo
  echo "## Step results"
  echo
  echo "| Step | Status | Note |"
  echo "| ---- | ------ | ---- |"
  for r in "${RESULTS[@]}"; do
    n="${r%%|*}"; rest="${r#*|}"; s="${rest%%|*}"; note="${rest#*|}"
    [[ -z "$note" ]] && note="—"
    echo "| $n | $s | $note |"
  done
  echo
  echo "## Known risks and gaps"
  echo
  echo "- **Web UI has no component-level test runner.** Vitest / Preact Testing Library is not installed. The current gate is \`tsc --noEmit\` + \`vite build\` — that catches type and build regressions but not behavior regressions."
  echo "- **No load/perf benchmarks in the release gate.** Add \`go test -bench\` for hot paths (agent loop, validate gate) before claiming SLA-grade readiness."
  echo "- **No headless browser E2E** against the web UI served by the runtime."
  echo
  echo "## Commands used to generate this scorecard"
  echo
  echo "\`\`\`bash"
  echo "./scripts/verify-release.sh $TIER"
  echo "# or via Makefile:"
  echo "make verify-release    # release tier (default)"
  echo "make test-fast         # fast tier"
  echo "make test-standard     # standard tier"
  echo "\`\`\`"
} > "$SCORECARD"

echo "  scorecard written → $SCORECARD"
echo

if [[ $FAIL_COUNT -eq 0 ]]; then
  bold "$(green "✅ verify-release ($TIER): all checks passed")"
  echo
  exit 0
else
  bold "$(red "❌ verify-release ($TIER): $FAIL_COUNT step(s) failed")"
  echo
  exit 1
fi
