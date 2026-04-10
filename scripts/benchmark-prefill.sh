#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# benchmark-prefill.sh — MLX KV-cache prefill effectiveness test
#
# Both conditions start from the same baseline: model loaded and ready
# (health endpoint 200 OK). This eliminates model-load noise.
#
# COLD  — Model ready → test message immediately.
#         Full system-prompt + user tokens prefilled from scratch.
#
# WARM  — Model ready → send one dummy request directly to the MLX server
#         with just the system prompt, max_tokens=1 (warms KV cache).
#         Wait for it to finish → send test message.
#         Only user tokens need prefilling; system prompt tokens are cached.
#
# For the "system prompt" we use the actual Atlas MIND.md (fetched via /mind),
# which is the stable prefix Atlas sends on every turn.
#
# Usage:  ./scripts/benchmark-prefill.sh [N_RUNS]   (default: 3)
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

ATLAS_URL="http://localhost:1984"
N_RUNS="${1:-3}"
TEST_MSG="Reply with exactly three words: yes no maybe"

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
    local r; r=$(curl -sf "http://127.0.0.1:${MLX_PORT}/health" -o /dev/null -w "%{http_code}" 2>/dev/null || echo 000)
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
    local code; code=$(curl -sf -o /dev/null -w "%{http_code}" \
      "http://127.0.0.1:${MLX_PORT}/health" 2>/dev/null || echo 000)
    [[ "$code" == "200" ]] && { ok "Model ready (${i}s)"; return 0; }
    sleep 1; (( i++ ))
  done
  warn "Model not ready after 180s"
}

# ── warm cache directly via MLX server ────────────────────────────────────────
# Sends system-prompt-only request with max_tokens=1 to the MLX server.
# This bypasses Atlas entirely — clean, direct KV cache warm-up.
warm_kv_cache() {
  local mind_escaped
  mind_escaped=$(python3 -c "import json,sys; print(json.dumps(sys.argv[1]))" "$MIND_CONTENT")
  local payload="{\"model\":\"$MLX_MODEL\",\"messages\":[{\"role\":\"system\",\"content\":$mind_escaped}],\"max_tokens\":1,\"stream\":false}"
  local start_w; start_w=$(python3 -c "import time; print(int(time.time()*1000))")
  local code; code=$(curl -sf -o /dev/null -w "%{http_code}" \
    -X POST "http://127.0.0.1:${MLX_PORT}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "$payload" 2>/dev/null || echo 000)
  local end_w; end_w=$(python3 -c "import time; print(int(time.time()*1000))")
  if [[ "$code" == "200" ]]; then
    ok "KV cache warmed in $(( end_w - start_w )) ms (direct MLX request)"
  else
    warn "Warm request returned HTTP $code"
  fi
}

# ── TTFT measurement ──────────────────────────────────────────────────────────
measure_ttft() {
  local conv_id
  conv_id=$(python3 -c "import uuid; print(str(uuid.uuid4()))")
  local tmp; tmp=$(mktemp)

  # Open SSE stream first
  curl -s -N "$ATLAS_URL/message/stream?conversationID=$conv_id" \
    -H "Accept: text/event-stream" 2>/dev/null > "$tmp" &
  local sse_pid=$!
  sleep 0.15

  # POST message and start clock
  local start_ms; start_ms=$(python3 -c "import time; print(int(time.time()*1000))")
  curl -s -X POST "$ATLAS_URL/message" \
    -H "Content-Type: application/json" \
    -d "{\"message\":$(python3 -c "import json; print(json.dumps('$TEST_MSG'))"),\"conversationId\":\"$conv_id\"}" \
    >/dev/null 2>&1 &
  local post_pid=$!

  # Wait for first assistant_delta
  local found=0 end_ms waited=0
  while (( waited < 120000 )); do
    if grep -q '"assistant_delta"' "$tmp" 2>/dev/null; then
      end_ms=$(python3 -c "import time; print(int(time.time()*1000))")
      found=1; break
    fi
    sleep 0.05; (( waited += 50 ))
  done

  kill "$post_pid" "$sse_pid" 2>/dev/null || true
  wait "$post_pid" "$sse_pid" 2>/dev/null || true
  rm -f "$tmp"

  (( found == 1 )) && echo $(( end_ms - start_ms )) || echo -1
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
echo -e "  Model: $MLX_MODEL"
echo -e "  Both conditions: model fully loaded before test message"
hr; echo ""

# ── Condition A: COLD ─────────────────────────────────────────────────────────
echo -e "${BOLD}Condition A — Cold cache (no prefill)${NC}"
echo    "  Load model → test message (system prompt prefilled in-request)"
echo ""

COLD=()
for (( i=1; i<=N_RUNS; i++ )); do
  echo -n "  Run $i/$N_RUNS  "
  stop_mlx
  start_mlx_ready
  ms=$(measure_ttft)
  echo "→ ${ms} ms"
  COLD+=("$ms")
  stop_mlx; sleep 2
done

echo ""; echo -e "${YELLOW}Cold results:${NC}"; print_stats "${COLD[@]}"

# ── Condition B: WARM ─────────────────────────────────────────────────────────
echo ""; echo -e "${BOLD}Condition B — Warm cache (explicit KV prefill)${NC}"
echo    "  Load model → warm KV cache directly → test message (only user tokens prefilled)"
echo ""

WARM=()
for (( i=1; i<=N_RUNS; i++ )); do
  echo -n "  Run $i/$N_RUNS  "
  stop_mlx
  start_mlx_ready
  warm_kv_cache          # directly warms the KV cache via MLX server
  ms=$(measure_ttft)
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
print(f"  Cold mean TTFT  : {cm:>7.0f} ms  (no prefill)")
print(f"  Warm mean TTFT  : {wm:>7.0f} ms  (with prefill)")
print(f"  Savings         : {d:>7.0f} ms  ({p:.1f}% faster)")
print()
if p > 10:   print("  ✅  Prefill is clearly effective.")
elif p > 0:  print("  ⚠️   Marginal gain — system prompt may be short or model is fast.")
else:        print("  ❌  No improvement — model may not support prompt caching.")
PYEOF
echo ""
