#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${ATLAS_BASE_URL:-http://127.0.0.1:1984}"
REPORT_DIR="${ATLAS_BENCHMARK_DIR:-reports/benchmarks}"
TIMESTAMP="$(date -u +"%Y%m%dT%H%M%SZ")"
JSON_OUT=""
MD_OUT=""
QUIET=false

usage() {
  cat <<'EOF'
Usage:
  scripts/benchmark-chat.sh [--report-dir DIR] [--json-out FILE] [--md-out FILE] [--quiet]

Defaults:
  --report-dir  reports/benchmarks
  --json-out    <report-dir>/chat-benchmark-<timestamp>.json
  --md-out      <report-dir>/chat-benchmark-<timestamp>.md

Environment:
  ATLAS_BASE_URL        Base URL for the installed Atlas daemon
  ATLAS_BENCHMARK_DIR   Default report directory
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --report-dir)
      REPORT_DIR="${2:?missing value for --report-dir}"
      shift 2
      ;;
    --json-out)
      JSON_OUT="${2:?missing value for --json-out}"
      shift 2
      ;;
    --md-out)
      MD_OUT="${2:?missing value for --md-out}"
      shift 2
      ;;
    --quiet)
      QUIET=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

for bin in curl jq perl column; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "Missing required dependency: $bin" >&2
    exit 1
  fi
done

mkdir -p "$REPORT_DIR"
if [[ -z "$JSON_OUT" ]]; then
  JSON_OUT="$REPORT_DIR/chat-benchmark-$TIMESTAMP.json"
fi
if [[ -z "$MD_OUT" ]]; then
  MD_OUT="$REPORT_DIR/chat-benchmark-$TIMESTAMP.md"
fi

now_ms() {
  perl -MTime::HiRes=time -e 'printf("%.0f\n", time()*1000)'
}

read -r -d '' CASES <<'JSON' || true
[
  {"id":"chat","prompt":"Hi Atlas.","expect":["Hi","help"]},
  {"id":"time","prompt":"What time is it in Tokyo right now?","expect":["Tokyo"]},
  {"id":"weather","prompt":"What will the weather be in Orlando tomorrow?","expect":["Orlando"]},
  {"id":"weather_web","prompt":"What is the weather in Paris right now, and name one famous museum there.","expect":["Paris","museum"]},
  {"id":"web_verify","prompt":"Who is the current OpenAI CEO? Verify from the official website.","expect":["Sam Altman","OpenAI"]},
  {"id":"finance","prompt":"Convert 100 USD to EUR.","expect":["EUR"]},
  {"id":"files","prompt":"Read /etc/hosts and summarize it.","expect":["approved","root"]},
  {"id":"automation_upsert","prompt":"Set up a daily Orlando weather forecast on Telegram at 8 AM called Benchmark Orlando Weather.","expect":["Benchmark Orlando Weather","8 AM"]},
  {"id":"automation_update","prompt":"Update my Benchmark Orlando Weather automation to use a more playful tone.","expect":["Benchmark Orlando Weather","updated"]},
  {"id":"plan","prompt":"Help me plan today.","expect":["today"]},
  {"id":"research","prompt":"Research the latest Apple Vision Pro app guidelines and keep it concise.","expect":["guidelines"]},
  {"id":"execution","prompt":"Explain briefly how you would update a REST client to add retry logic.","expect":["retry"]}
]
JSON

RESULTS="[]"
RUN_STARTED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

while IFS= read -r case_json; do
  [[ -z "$case_json" ]] && continue
  id="$(jq -r '.id' <<<"$case_json")"
  prompt="$(jq -r '.prompt' <<<"$case_json")"
  started_at_iso="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  started_ms="$(now_ms)"

  payload="$(jq -nc --arg message "$prompt" '{"message":$message,"platform":"benchmark"}')"
  response="$(curl -sS -X POST "$BASE_URL/message" -H 'Content-Type: application/json' --data "$payload")"

  ended_ms="$(now_ms)"
  latency_ms="$((ended_ms - started_ms))"
  conv_id="$(jq -r '.conversation.id // empty' <<<"$response")"
  status="$(jq -r '.response.status // empty' <<<"$response")"
  answer="$(jq -r '.response.assistantMessage // .response.errorMessage // empty' <<<"$response")"

  usage_response="$(curl -sS --get "$BASE_URL/usage/events" --data-urlencode "since=$started_at_iso" --data "limit=500")"
  usage_event="$(jq -c --arg conv "$conv_id" '[.events[] | select(.conversationId == $conv)] | sort_by(.recordedAt) | last // {}' <<<"$usage_response")"

  input_tokens="$(jq -r '.inputTokens // 0' <<<"$usage_event")"
  output_tokens="$(jq -r '.outputTokens // 0' <<<"$usage_event")"
  total_tokens="$((input_tokens + output_tokens))"

  pass=true
  while IFS= read -r expected; do
    [[ -z "$expected" ]] && continue
    if ! grep -Fqi "$expected" <<<"$answer"; then
      pass=false
      break
    fi
  done < <(jq -r '.expect[]?' <<<"$case_json")

  result_row="$(jq -nc \
    --arg id "$id" \
    --arg prompt "$prompt" \
    --arg status "$status" \
    --arg answer "$answer" \
    --arg conv "$conv_id" \
    --arg startedAt "$started_at_iso" \
    --argjson latency "$latency_ms" \
    --argjson input "$input_tokens" \
    --argjson output "$output_tokens" \
    --argjson total "$total_tokens" \
    --argjson pass "$( [ "$pass" = true ] && echo true || echo false )" \
    '{
      id: $id,
      prompt: $prompt,
      conversationId: $conv,
      startedAt: $startedAt,
      status: $status,
      latencyMs: $latency,
      inputTokens: $input,
      outputTokens: $output,
      totalTokens: $total,
      heuristicPass: $pass,
      answer: $answer
    }')"
  RESULTS="$(jq --argjson row "$result_row" '. + [$row]' <<<"$RESULTS")"

  if [[ "$QUIET" != true ]]; then
    printf '%-18s %8sms %8s tokens  %s\n' "$id" "$latency_ms" "$total_tokens" "$status"
  fi
done < <(jq -c '.[]' <<<"$CASES")

SUMMARY="$(jq -nc \
  --arg baseUrl "$BASE_URL" \
  --arg startedAt "$RUN_STARTED_AT" \
  --arg finishedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --argjson results "$RESULTS" \
  '{
    baseUrl: $baseUrl,
    startedAt: $startedAt,
    finishedAt: $finishedAt,
    runs: ($results | length),
    avgLatencyMs: (if ($results | length) == 0 then 0 else (($results | map(.latencyMs) | add) / ($results | length) | floor) end),
    avgInputTokens: (if ($results | length) == 0 then 0 else (($results | map(.inputTokens) | add) / ($results | length) | floor) end),
    avgOutputTokens: (if ($results | length) == 0 then 0 else (($results | map(.outputTokens) | add) / ($results | length) | floor) end),
    avgTotalTokens: (if ($results | length) == 0 then 0 else (($results | map(.totalTokens) | add) / ($results | length) | floor) end),
    passRate: (if ($results | length) == 0 then 0 else (((($results | map(select(.heuristicPass)) | length) / ($results | length)) * 10000) | round / 100) end)
  }')"

jq -n \
  --arg generatedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --arg script "scripts/benchmark-chat.sh" \
  --argjson summary "$SUMMARY" \
  --argjson cases "$RESULTS" \
  '{
    generatedAt: $generatedAt,
    script: $script,
    summary: $summary,
    cases: $cases
  }' > "$JSON_OUT"

{
  echo "# Chat Benchmark"
  echo
  echo "- Generated at: $(jq -r '.generatedAt' "$JSON_OUT")"
  echo "- Base URL: $(jq -r '.summary.baseUrl' "$JSON_OUT")"
  echo "- Runs: $(jq -r '.summary.runs' "$JSON_OUT")"
  echo "- Avg latency: $(jq -r '.summary.avgLatencyMs' "$JSON_OUT") ms"
  echo "- Avg input tokens: $(jq -r '.summary.avgInputTokens' "$JSON_OUT")"
  echo "- Avg output tokens: $(jq -r '.summary.avgOutputTokens' "$JSON_OUT")"
  echo "- Avg total tokens: $(jq -r '.summary.avgTotalTokens' "$JSON_OUT")"
  echo "- Heuristic pass rate: $(jq -r '.summary.passRate' "$JSON_OUT")%"
  echo
  echo "| Case | Latency (ms) | Input | Output | Total | Pass | Answer |"
  echo "|---|---:|---:|---:|---:|---|---|"
  jq -r '.cases[] | [
      .id,
      (.latencyMs|tostring),
      (.inputTokens|tostring),
      (.outputTokens|tostring),
      (.totalTokens|tostring),
      (if .heuristicPass then "yes" else "no" end),
      (.answer | gsub("[\r\n]+"; " ") | gsub("\\|"; "\\\\|") | .[:180])
    ] | "| " + join(" | ") + " |"' "$JSON_OUT"
} > "$MD_OUT"

if [[ "$QUIET" != true ]]; then
  echo
  echo "Results:"
  jq -r '
    (["id","latency_ms","input_tokens","output_tokens","total_tokens","pass","answer"],
     (.cases[] | [.id, (.latencyMs|tostring), (.inputTokens|tostring), (.outputTokens|tostring), (.totalTokens|tostring), (.heuristicPass|tostring), (.answer | gsub("[\\r\\n]+";" ") | .[:120])]))
    | @tsv
  ' "$JSON_OUT" | column -t -s $'\t'

  echo
  echo "Summary:"
  jq '.summary' "$JSON_OUT"
  echo
  echo "Wrote:"
  echo "- $JSON_OUT"
  echo "- $MD_OUT"
fi
