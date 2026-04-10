#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# benchmark-prefill.sh — MLX KV-cache prefill effectiveness test
#
# Measures Time-To-First-Token (TTFT) directly on the MLX server.
# Both conditions are symmetric: model fully loaded and idle before each run.
#
# COLD  — Model fresh → full system-prompt + user message in one request.
#         All tokens must be prefilled from scratch.
#
# WARM  — Model fresh → system-prompt-only warm-up (max_tokens=1, ~instant).
#         Then same full request.
#         System-prompt tokens are already in KV cache; only user tokens prefill.
#
# Both conditions hit the MLX server directly (/v1/chat/completions).
# No Atlas SSE complexity. Clean, direct cache measurement.
#
# Usage:  ./scripts/benchmark-prefill.sh [N_RUNS]   (default: 3)
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

ATLAS_URL="http://localhost:1984"
N_RUNS="${1:-3}"
TEST_MSG="What is 2 plus 2?"

CYAN='\033[0;36m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
RED='\033[0;31m'; BOLD='\033[1m'; NC='\033[0m'

log()  { echo -e "${CYAN}[bench]${NC} $*" >&2; }
ok()   { echo -e "${GREEN}  ✓${NC} $*" >&2; }
warn() { echo -e "${YELLOW}  !${NC} $*" >&2; }
err()  { echo -e "${RED}  ✗${NC} $*" >&2; exit 1; }
hr()   { echo -e "${BOLD}────────────────────────────────────────────────${NC}"; }

for cmd in curl python3; do
  command -v "$cmd" >/dev/null 2>&1 || err "Required: $cmd"
done
curl -sf "$ATLAS_URL/status" >/dev/null 2>&1 || err "Atlas not running at $ATLAS_URL"

# ── config ────────────────────────────────────────────────────────────────────
MLX_MODEL=$(curl -sf "$ATLAS_URL/config" \
  | python3 -c "import sys,json,os; c=json.load(sys.stdin); print(os.path.basename(c.get('selectedAtlasMLXModel','')))")
MLX_PORT=$(curl -sf "$ATLAS_URL/config" \
  | python3 -c "import sys,json; c=json.load(sys.stdin); print(c.get('atlasMLXPort',11990))")
[[ -z "$MLX_MODEL" || "$MLX_MODEL" == "." ]] && err "No MLX model configured."

# Fetch the actual MIND.md as the stable system-prompt prefix Atlas uses
MIND_CONTENT=$(curl -sf "$ATLAS_URL/mind" \
  | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('content',''))" 2>/dev/null || echo "")
[[ -z "$MIND_CONTENT" ]] && MIND_CONTENT="You are Atlas, a helpful AI assistant."
MIND_LEN=${#MIND_CONTENT}

log "Model      : $MLX_MODEL (port $MLX_PORT)"
log "MIND chars : $MIND_LEN"
log "Runs       : $N_RUNS per condition"

# ── MLX control ───────────────────────────────────────────────────────────────
stop_mlx() {
  curl -sf -X POST "$ATLAS_URL/engine/mlx/stop" \
    -H "Content-Type: application/json" -d '{}' >/dev/null 2>&1 || true
  local i=0
  while (( i < 40 )); do
    local r; r=$(curl -s -o /dev/null -w "%{http_code}" \
      "http://127.0.0.1:${MLX_PORT}/health" 2>/dev/null || echo 000)
    [[ "$r" != "200" ]] && return 0
    sleep 0.5; (( i++ ))
  done
  warn "MLX server may still be responding"
}

# Start via engine API and wait until health endpoint confirms model is loaded
start_mlx_ready() {
  curl -sf -X POST "$ATLAS_URL/engine/mlx/start" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$MLX_MODEL\",\"port\":$MLX_PORT}" >/dev/null 2>&1 || true
  local i=0
  while (( i < 180 )); do
    local code; code=$(curl -s -o /dev/null -w "%{http_code}" \
      "http://127.0.0.1:${MLX_PORT}/health" 2>/dev/null || echo 000)
    [[ "$code" == "200" ]] && { ok "Model ready (${i}s)"; return 0; }
    sleep 1; (( i++ ))
  done
  err "Model not ready after 180s"
}

# ── direct MLX TTFT measurement ───────────────────────────────────────────────
# Sends full conversation (system + user) directly to MLX, returns first-token ms.
# Note: mlx_lm.server doesn't expose first-token time in the API response,
# so we measure wall-clock time for the complete non-streaming response.
# For a short test message (few tokens output), this closely approximates TTFT.
measure_ttft_direct() {
  local extra_pre="$1"   # optional warm-up payload (empty = no warm-up)
  local MLX_URL="http://127.0.0.1:${MLX_PORT}/v1/chat/completions"

  # Build escaping of content
  local mind_j; mind_j=$(python3 -c "import json,sys; print(json.dumps(sys.argv[1]))" "$MIND_CONTENT")
  local msg_j;  msg_j=$(python3 -c  "import json,sys; print(json.dumps(sys.argv[1]))" "$TEST_MSG")

  # Optional pre-warm (system-prompt-only, max_tokens=1)
  if [[ -n "$extra_pre" ]]; then
    local warm_payload="{\"messages\":[{\"role\":\"system\",\"content\":$mind_j}],\"max_tokens\":1,\"stream\":false}"
    local wt_start; wt_start=$(python3 -c "import time; print(int(time.time()*1000))")
    local wcode; wcode=$(curl -s -o /dev/null -w "%{http_code}" \
      -X POST "$MLX_URL" -H "Content-Type: application/json" \
      -d "$warm_payload" 2>/dev/null)
    local wt_end; wt_end=$(python3 -c "import time; print(int(time.time()*1000))")
    if [[ "$wcode" == "200" ]]; then
      ok "KV cache warmed in $(( wt_end - wt_start )) ms (HTTP $wcode)"
    else
      warn "Warm request returned HTTP $wcode"
    fi
  fi

  # Full-conversation request (non-streaming, max_tokens=8 — enough for short answer)
  local full_payload="{\"messages\":[{\"role\":\"system\",\"content\":$mind_j},{\"role\":\"user\",\"content\":$msg_j}],\"max_tokens\":8,\"stream\":false}"
  local t_start; t_start=$(python3 -c "import time; print(int(time.time()*1000))")
  local resp; resp=$(curl -s -X POST "$MLX_URL" \
    -H "Content-Type: application/json" \
    -d "$full_payload" 2>/dev/null)
  local t_end; t_end=$(python3 -c "import time; print(int(time.time()*1000))")

  # Check success and extract cached_tokens for visibility
  local ok_check; ok_check=$(echo "$resp" | python3 -c "import sys,json; r=json.load(sys.stdin); u=r.get('usage',{}); cached=u.get('prompt_tokens_details',{}).get('cached_tokens',0); total=u.get('prompt_tokens',0); print(f'cached={cached}/{total}')" 2>/dev/null || echo "error")
  if [[ "$ok_check" == "error" ]]; then
    warn "Bad response: $(echo "$resp" | head -c 80)"
    echo -1
    return
  fi
  echo "  [${ok_check} tokens cached]" >&2
  echo $(( t_end - t_start ))
}

# ── stats ─────────────────────────────────────────────────────────────────────
print_stats() {
  python3 - "$@" <<'PYEOF'
import sys
vals=[int(x) for x in sys.argv[1:] if int(x)>0]
if not vals:
    print("    (no valid measurements)")
else:
    print(f"    min  : {min(vals)} ms")
    print(f"    mean : {sum(vals)/len(vals):.0f} ms")
    print(f"    max  : {max(vals)} ms")
    print(f"    raw  : {vals} ms")
PYEOF
}

# ═════════════════════════════════════════════════════════════════════════════
echo ""; hr
echo -e "${BOLD}  MLX Prefill Benchmark — ${N_RUNS} runs × 2 conditions${NC}"
echo -e "  Model : $MLX_MODEL   Mind : ${MIND_LEN} chars"
echo -e "  Both conditions: model fully loaded, idle, before each measurement"
hr; echo ""
echo -e "  NOTE: Measuring wall-clock time on MLX server directly (no Atlas SSE)."
echo -e "  max_tokens=8 so response time ≈ prefill time (decode is negligible)."
echo ""

# ── Condition A: COLD ─────────────────────────────────────────────────────────
echo -e "${BOLD}Condition A — Cold cache (full prefill in-request)${NC}"
echo ""

COLD=()
for (( i=1; i<=N_RUNS; i++ )); do
  echo -n "  Run $i/$N_RUNS  "
  stop_mlx
  start_mlx_ready
  ms=$(measure_ttft_direct "")
  echo "→ ${ms} ms"
  COLD+=("$ms")
  stop_mlx; sleep 2
done

echo ""; echo -e "${YELLOW}Cold results:${NC}"; print_stats "${COLD[@]}"

# ── Condition B: WARM ─────────────────────────────────────────────────────────
echo ""; echo -e "${BOLD}Condition B — Warm cache (system prompt pre-cached)${NC}"
echo ""

WARM=()
for (( i=1; i<=N_RUNS; i++ )); do
  echo -n "  Run $i/$N_RUNS  "
  stop_mlx
  start_mlx_ready
  ms=$(measure_ttft_direct "warm")   # triggers KV warm-up before measuring
  echo "→ ${ms} ms"
  WARM+=("$ms")
  stop_mlx; sleep 2
done

echo ""; echo -e "${GREEN}Warm results:${NC}"; print_stats "${WARM[@]}"

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""; hr; echo -e "${BOLD}  Summary${NC}"; hr; echo ""

python3 - "${COLD[@]}" "---" "${WARM[@]}" <<'PYEOF'
import sys
args=sys.argv[1:]
sep=args.index("---")
cold=[int(x) for x in args[:sep]   if int(x)>0]
warm=[int(x) for x in args[sep+1:] if int(x)>0]
if not cold or not warm:
    print("  Not enough valid data."); sys.exit(0)
cm=sum(cold)/len(cold); wm=sum(warm)/len(warm)
d=cm-wm; p=d/cm*100 if cm else 0
print(f"  Cold mean  : {cm:>7.0f} ms  (no prefill — full {len(cold)}-token system prompt)")
print(f"  Warm mean  : {wm:>7.0f} ms  (system prompt in KV cache)")
print(f"  Savings    : {d:>7.0f} ms  ({p:.1f}% faster first response)")
print()
if p > 15:   print("  ✅  Prefill is clearly effective — significant TTFT reduction.")
elif p > 5:  print("  ✅  Prefill is effective.")
elif p > 0:  print("  ⚠️   Marginal gain — system prompt may be short or model is very fast.")
else:        print("  ❌  No improvement detected.")
PYEOF
echo ""
