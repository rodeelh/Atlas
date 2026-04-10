#!/usr/bin/env bash
set -euo pipefail

ATLAS_URL="${ATLAS_URL:-http://localhost:1984}"
SERIAL_RUNS="${1:-3}"
CONCURRENT_RUNS="${2:-3}"
CONCURRENCY="${3:-4}"
TEST_MSG="${TEST_MSG:-Reply with exactly six words about local inference.}"

log() { printf '[bench] %s\n' "$*"; }
die() { printf '[bench] error: %s\n' "$*" >&2; exit 1; }

for cmd in curl python3; do
  command -v "$cmd" >/dev/null 2>&1 || die "missing required command: $cmd"
done

curl -sf "$ATLAS_URL/status" >/dev/null || die "Atlas is not running at $ATLAS_URL"

ACTIVE_PROVIDER=$(curl -sf "$ATLAS_URL/config" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("activeAIProvider",""))')
if [[ "$ACTIVE_PROVIDER" != "atlas_mlx" ]]; then
  die "activeAIProvider must be atlas_mlx for this benchmark (got: ${ACTIVE_PROVIDER:-unset})"
fi

configure_scheduler() {
  local max_concurrency="$1"
  local batch_window_ms="$2"
  curl -sf -X POST "$ATLAS_URL/engine/mlx/scheduler" \
    -H "Content-Type: application/json" \
    -d "{\"maxConcurrency\":$max_concurrency,\"batchWindowMs\":$batch_window_ms}" >/dev/null
}

stop_mlx() {
  curl -sf -X POST "$ATLAS_URL/engine/mlx/stop" \
    -H "Content-Type: application/json" \
    -d '{}' >/dev/null 2>&1 || true
  sleep 1
}

warm_model() {
  local conv_id
  conv_id=$(python3 -c 'import uuid; print(uuid.uuid4())')
  curl -sf -X POST "$ATLAS_URL/message" \
    -H "Content-Type: application/json" \
    -d "{\"message\":\"hello\",\"conversationId\":\"$conv_id\"}" >/dev/null 2>&1 || true
  sleep 2
}

run_case() {
  local label="$1"
  local runs="$2"
  local concurrency="$3"

  ATLAS_URL="$ATLAS_URL" TEST_MSG="$TEST_MSG" RUNS="$runs" CONCURRENCY="$concurrency" LABEL="$label" \
  python3 <<'PY'
import json
import os
import threading
import time
import urllib.request
import urllib.parse
import uuid

atlas_url = os.environ["ATLAS_URL"].rstrip("/")
test_msg = os.environ["TEST_MSG"]
runs = int(os.environ["RUNS"])
concurrency = int(os.environ["CONCURRENCY"])
label = os.environ["LABEL"]

def one_request(idx: int, barrier: threading.Barrier, out: list):
    conv_id = str(uuid.uuid4())
    stream_url = atlas_url + "/message/stream?conversationID=" + urllib.parse.quote(conv_id)
    send_payload = json.dumps({"message": test_msg, "conversationId": conv_id}).encode()
    send_req = urllib.request.Request(
        atlas_url + "/message",
        data=send_payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    barrier.wait()
    start = time.perf_counter()
    first_token = None
    send_error = []

    with urllib.request.urlopen(stream_url, timeout=180) as stream_resp:
        def send_message():
            try:
                with urllib.request.urlopen(send_req, timeout=180) as _:
                    pass
            except Exception as exc:
                send_error.append(str(exc))

        sender = threading.Thread(target=send_message)
        sender.start()
        for raw in stream_resp:
            line = raw.decode("utf-8", "ignore").strip()
            if not line.startswith("data: "):
                continue
            try:
                event = json.loads(line[6:])
            except json.JSONDecodeError:
                continue
            if event.get("type") == "assistant_delta" and first_token is None:
                first_token = (time.perf_counter() - start) * 1000.0
            if event.get("type") == "done":
                break
        sender.join()
    total = (time.perf_counter() - start) * 1000.0
    out[idx] = {
        "ttft_ms": round(first_token or -1.0, 1),
        "total_ms": round(total, 1),
        "error": send_error[0] if send_error else "",
    }

all_ttft = []
all_total = []
group_peaks = []

for _ in range(runs):
    barrier = threading.Barrier(concurrency)
    out = [None] * concurrency
    threads = [threading.Thread(target=one_request, args=(i, barrier, out)) for i in range(concurrency)]
    group_start = time.perf_counter()
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    group_elapsed = (time.perf_counter() - group_start) * 1000.0
    group_peaks.append(round(group_elapsed, 1))
    for item in out:
        if item["error"]:
            raise RuntimeError(item["error"])
        all_ttft.append(item["ttft_ms"])
        all_total.append(item["total_ms"])

print(json.dumps({
    "label": label,
    "concurrency": concurrency,
    "runs": runs,
    "ttft_ms": all_ttft,
    "total_ms": all_total,
    "group_elapsed_ms": group_peaks,
}))
PY
}

print_stats() {
  local label="$1"
  local json_blob="$2"
  python3 - "$label" "$json_blob" <<'PY'
import json
import statistics
import sys

label = sys.argv[1]
payload = json.loads(sys.argv[2])

def fmt(vals):
    vals = [v for v in vals if v >= 0]
    if not vals:
        return "no valid samples"
    return f"min={min(vals):.1f} mean={statistics.fmean(vals):.1f} p95={sorted(vals)[max(0, int(len(vals)*0.95)-1)]:.1f} max={max(vals):.1f}"

print(f"{label}")
print(f"  TTFT : {fmt(payload['ttft_ms'])}")
print(f"  total: {fmt(payload['total_ms'])}")
print(f"  wave : {fmt(payload['group_elapsed_ms'])}")
print(f"  raw ttft : {payload['ttft_ms']}")
print(f"  raw total: {payload['total_ms']}")
PY
}

log "Benchmarking MLX scheduler on $ATLAS_URL"
log "Serial runs: $SERIAL_RUNS, concurrent waves: $CONCURRENT_RUNS, concurrency: $CONCURRENCY"

stop_mlx
configure_scheduler 1 0
warm_model
BEFORE_SERIAL=$(run_case "before-serial" "$SERIAL_RUNS" 1)
BEFORE_CONCURRENT=$(run_case "before-concurrent" "$CONCURRENT_RUNS" "$CONCURRENCY")

stop_mlx
configure_scheduler 2 12
warm_model
AFTER_SERIAL=$(run_case "after-serial" "$SERIAL_RUNS" 1)
AFTER_CONCURRENT=$(run_case "after-concurrent" "$CONCURRENT_RUNS" "$CONCURRENCY")

printf '\n'
print_stats "Before: serial (maxConcurrency=1, batchWindowMs=0)" "$BEFORE_SERIAL"
printf '\n'
print_stats "Before: concurrent (maxConcurrency=1, batchWindowMs=0)" "$BEFORE_CONCURRENT"
printf '\n'
print_stats "After: serial (maxConcurrency=2, batchWindowMs=12)" "$AFTER_SERIAL"
printf '\n'
print_stats "After: concurrent (maxConcurrency=2, batchWindowMs=12)" "$AFTER_CONCURRENT"
