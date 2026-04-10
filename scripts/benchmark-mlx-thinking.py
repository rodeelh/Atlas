#!/usr/bin/env python3
"""
benchmark-mlx-thinking.py
──────────────────────────────────────────────────────────────────────────────
Runs 10 prompts against the Atlas MLX server twice each — once with thinking
enabled (enable_thinking=true in chat_template_kwargs) and once without.

Scores each response on three dimensions:
  Quality   0–3   keyword/answer accuracy (correct answer found = 2, good
                  reasoning present = +1, partial credit = 1, wrong = 0)
  Latency   ms    time to first output token (TTFT)
  TPS       tok/s output tokens ÷ generation time (thinking tokens excluded)

Usage:
  python3 scripts/benchmark-mlx-thinking.py [--port 11990] [--max-tokens 400]
──────────────────────────────────────────────────────────────────────────────
"""

import argparse
import json
import re
import sys
import time
import urllib.request
from dataclasses import dataclass, field
from typing import Optional

# ── ANSI colours ──────────────────────────────────────────────────────────────
CYAN   = "\033[0;36m"
GREEN  = "\033[0;32m"
YELLOW = "\033[1;33m"
RED    = "\033[0;31m"
BOLD   = "\033[1m"
DIM    = "\033[2m"
NC     = "\033[0m"

def c(colour: str, text: str) -> str:
    return f"{colour}{text}{NC}"

def hr(char="─", width=72):
    print(c(BOLD, char * width))

# ── Prompts ───────────────────────────────────────────────────────────────────
# Each entry: (label, prompt, required_keywords, bonus_keywords)
#   required_keywords — ALL must appear for full quality score (2 pts)
#   bonus_keywords    — ANY match adds +1 reasoning bonus
PROMPTS = [
    (
        "Arithmetic",
        "What is 17 × 24? Show your working.",
        ["408"],
        ["multiply", "product", "×", "*"],
    ),
    (
        "Cognitive bias",
        (
            "A bat and a ball cost $1.10 in total. "
            "The bat costs $1.00 more than the ball. "
            "How much does the ball cost?"
        ),
        ["5"],   # 5 cents / $0.05
        ["cent", "0.05", "not 10", "not $0.10"],
    ),
    (
        "Capital city",
        "What is the capital of Australia? Many people say Sydney — is that correct?",
        ["canberra"],
        ["not sydney", "incorrect", "wrong", "1913", "federal"],
    ),
    (
        "Fibonacci",
        "What is the next number in this sequence: 1, 1, 2, 3, 5, 8, 13, ___?",
        ["21"],
        ["fibonacci", "sum", "previous two", "add"],
    ),
    (
        "Logic (syllogism)",
        (
            "All roses are flowers. Some flowers fade quickly. "
            "Can we conclude that all roses fade quickly? Why or why not?"
        ),
        ["no"],
        ["not necessarily", "invalid", "cannot", "some", "doesn't follow"],
    ),
    (
        "Speed/distance",
        (
            "Train A leaves at 9 AM travelling at 60 mph. "
            "Train B leaves the same station at 10 AM travelling at 80 mph. "
            "At what time does Train B catch Train A?"
        ),
        ["1"],   # 1 pm / 13:00 — gap is 60 miles, closing at 20 mph → 3 hrs from 10am = 1pm
        ["pm", "13:00", "3 hours", "three hours", "1:00"],
    ),
    (
        "Probability",
        (
            "A fair coin is flipped 3 times. "
            "What is the probability of getting exactly 2 heads?"
        ),
        ["3/8", "0.375", "37.5"],
        ["combinations", "binomial", "3 ways", "HHT", "HTH", "THH"],
    ),
    (
        "Riddle",
        (
            "What has cities but no houses, mountains but no trees, "
            "and water but no fish?"
        ),
        ["map"],
        ["atlas", "chart", "diagram"],
    ),
    (
        "Number pattern",
        "What comes next in this series: 2, 6, 12, 20, 30, ___? Explain the pattern.",
        ["42"],
        ["n(n+1)", "n×(n+1)", "product", "consecutive", "1×2", "2×3"],
    ),
    (
        "Causal inference",
        (
            "If it rains, the ground gets wet. "
            "The ground is wet. Did it necessarily rain? Explain."
        ),
        ["no"],
        ["not necessarily", "other", "sprinkler", "hose", "pipe", "affirming the consequent",
         "fallacy", "could be", "might be"],
    ),
]

# ── Streaming helper ──────────────────────────────────────────────────────────

@dataclass
class StreamResult:
    text: str = ""
    thinking: str = ""
    ttft_ms: float = 0.0
    gen_ms: float = 0.0       # time spent on output tokens (excl. thinking)
    output_tokens: int = 0
    prompt_tokens: int = 0
    error: Optional[str] = None


def stream_completion(url: str, model: str, prompt: str,
                      thinking: bool, max_tokens: int) -> StreamResult:
    """Stream one completion and return timing + content."""
    msgs = [{"role": "user", "content": prompt}]
    body: dict = {
        "model": model,
        "messages": msgs,
        "max_tokens": max_tokens,
        "stream": True,
        "stream_options": {"include_usage": True},
    }
    if thinking:
        body["chat_template_kwargs"] = {"enable_thinking": True}

    payload = json.dumps(body).encode()
    req = urllib.request.Request(
        f"{url}/v1/chat/completions",
        data=payload,
        headers={"Content-Type": "application/json"},
    )

    result = StreamResult()
    t_request = time.perf_counter()

    # mlx-lm splits thinking vs output into separate SSE fields:
    #   delta.reasoning  — tokens produced while in the reasoning/thinking state
    #   delta.content    — tokens produced in normal (output) state
    # <think> tags never appear in delta.content — we must read delta.reasoning.

    first_output_token_seen = False   # first delta.content token (TTFT for output)
    first_any_token_seen = False      # first token of any kind (thinking or output)
    t_thinking_start: Optional[float] = None
    t_thinking_end: Optional[float] = None

    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            for raw in resp:
                line = raw.decode("utf-8").rstrip()
                if not line.startswith("data: "):
                    continue
                data = line[6:]
                if data == "[DONE]":
                    break
                try:
                    chunk = json.loads(data)
                except json.JSONDecodeError:
                    continue

                # Usage chunk (stream_options include_usage — arrives before [DONE])
                if "usage" in chunk and "choices" not in chunk:
                    result.prompt_tokens = chunk["usage"].get("prompt_tokens", 0)
                    result.output_tokens = chunk["usage"].get("completion_tokens", 0)
                    continue

                choices = chunk.get("choices", [])
                if not choices:
                    continue

                delta = choices[0].get("delta", {})
                reasoning = delta.get("reasoning") or ""
                content   = delta.get("content") or ""
                now = time.perf_counter()

                # --- Thinking (reasoning) tokens ---
                if reasoning:
                    if not first_any_token_seen:
                        first_any_token_seen = True
                        t_thinking_start = now
                    result.thinking += reasoning
                    t_thinking_end = now   # keep updating; last reasoning token marks end

                # --- Output (content) tokens ---
                if content:
                    if not first_output_token_seen:
                        first_output_token_seen = True
                        result.ttft_ms = (now - t_request) * 1000
                    result.text += content

    except Exception as exc:
        result.error = str(exc)
        return result

    t_end = time.perf_counter()

    # gen_ms = time spent generating output tokens only (excludes thinking phase)
    if first_output_token_seen:
        output_start = t_thinking_end if t_thinking_end else t_request
        result.gen_ms = max((t_end - output_start) * 1000, 1)
    else:
        result.gen_ms = 1

    return result


# ── Quality scoring ───────────────────────────────────────────────────────────

def score_quality(text: str, required: list[str], bonus: list[str]) -> tuple[int, str]:
    """Return (score 0-3, reason string)."""
    lower = text.lower()

    matched_req = [kw for kw in required if kw.lower() in lower]
    matched_bon = [kw for kw in bonus  if kw.lower() in lower]

    if len(matched_req) == len(required) and len(required) > 0:
        score = 3 if matched_bon else 2
        note = f"✓ answer ({'+reasoning' if matched_bon else 'no reasoning bonus'})"
    elif matched_req:
        score = 1
        note = f"~ partial ({len(matched_req)}/{len(required)} keywords)"
    else:
        score = 0
        note = "✗ wrong/missing"

    return score, note


# ── Output helpers ────────────────────────────────────────────────────────────

def tps(result: StreamResult) -> float:
    """Output TPS excluding thinking tokens."""
    # Count output tokens excluding thinking block tokens
    clean = re.sub(r"<think>.*?</think>", "", result.text, flags=re.DOTALL)
    token_est = len(clean.split())  # rough word count as proxy
    if result.output_tokens > 0:
        # Use reported output tokens minus estimated thinking tokens
        think_words = len(result.thinking.split()) if result.thinking else 0
        out_toks = max(1, result.output_tokens - int(think_words * 1.3))
    else:
        out_toks = max(1, len(clean.split()))
    secs = result.gen_ms / 1000
    return out_toks / secs if secs > 0 else 0


def quality_bar(score: int) -> str:
    filled = "█" * score
    empty  = "░" * (3 - score)
    colour = GREEN if score == 3 else YELLOW if score >= 1 else RED
    return c(colour, filled) + c(DIM, empty)


def thinking_summary(result: StreamResult) -> str:
    if not result.thinking:
        return c(DIM, "none")
    words = len(result.thinking.split())
    return f"{words} words"


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--atlas-url", default="http://localhost:1984",
                        help="Atlas runtime URL (default: http://localhost:1984)")
    parser.add_argument("--port", type=int, default=None,
                        help="MLX server port (default: read from Atlas config)")
    parser.add_argument("--max-tokens", type=int, default=400,
                        help="Max output tokens per request (default: 400)")
    parser.add_argument("--prompts", type=str, default=None,
                        help="Comma-separated indices (1-based) to run subset, e.g. '1,3,5'")
    args = parser.parse_args()

    # Resolve model + port from Atlas config
    try:
        with urllib.request.urlopen(f"{args.atlas_url}/config", timeout=5) as r:
            cfg = json.load(r)
    except Exception as e:
        print(c(RED, f"Cannot reach Atlas at {args.atlas_url}: {e}"))
        sys.exit(1)

    model_name = cfg.get("selectedAtlasMLXModel", "")
    if not model_name:
        print(c(RED, "No MLX model configured in Atlas."))
        sys.exit(1)

    import os
    model_base = os.path.basename(model_name.rstrip("/"))
    mlx_port   = args.port or cfg.get("atlasMLXPort", 11990)
    mlx_url    = f"http://127.0.0.1:{mlx_port}"

    # Check MLX server is up
    try:
        with urllib.request.urlopen(f"{mlx_url}/health", timeout=5) as r:
            pass
    except Exception:
        print(c(RED, f"MLX server not responding at {mlx_url}. Start the engine first."))
        sys.exit(1)

    # Resolve the actual model ID the server uses (may be a full path)
    try:
        with urllib.request.urlopen(f"{mlx_url}/v1/models", timeout=5) as r:
            models_resp = json.load(r)
        model_ids = [m["id"] for m in models_resp.get("data", [])]
        # Prefer exact match, then suffix match (full path ending in model_base)
        resolved = next(
            (mid for mid in model_ids if mid == model_base or os.path.basename(mid.rstrip("/")) == model_base),
            model_ids[0] if model_ids else model_base,
        )
        model_base = resolved
    except Exception:
        pass  # fall back to basename

    # Select prompts subset
    selected = list(range(len(PROMPTS)))
    if args.prompts:
        try:
            selected = [int(i) - 1 for i in args.prompts.split(",")]
        except ValueError:
            print(c(RED, "Invalid --prompts value. Use comma-separated 1-based indices."))
            sys.exit(1)

    n = len(selected)

    # ── Header ────────────────────────────────────────────────────────────────
    print()
    hr("═")
    print(c(BOLD, f"  MLX Thinking Benchmark — {n} prompts × 2 conditions"))
    print(f"  Model : {model_base}   Port : {mlx_port}   Max tokens : {args.max_tokens}")
    hr("═")
    print()

    # ── Results storage ───────────────────────────────────────────────────────
    @dataclass
    class Row:
        label: str
        idx: int
        off: StreamResult = field(default_factory=StreamResult)
        on:  StreamResult = field(default_factory=StreamResult)
        q_off: int = 0
        q_on:  int = 0
        note_off: str = ""
        note_on:  str = ""

    rows: list[Row] = []

    for run_i, pi in enumerate(selected):
        label, prompt_text, required, bonus = PROMPTS[pi]
        row = Row(label=label, idx=pi)

        for condition in ("off", "on"):
            thinking_on = (condition == "on")
            tag = c(CYAN, "thinking=on ") if thinking_on else c(DIM,  "thinking=off")
            print(f"  [{run_i+1}/{n}] {c(BOLD, label):<30}  {tag}  … ", end="", flush=True)

            res = stream_completion(mlx_url, model_base, prompt_text,
                                    thinking_on, args.max_tokens)

            if res.error:
                print(c(RED, f"ERROR: {res.error}"))
                continue

            q, note = score_quality(res.text, required, bonus)
            speed = tps(res)
            print(f"TTFT {res.ttft_ms:>5.0f}ms  {speed:>5.1f} tok/s  Q={q}/3  {note}")

            if condition == "off":
                row.off, row.q_off, row.note_off = res, q, note
            else:
                row.on,  row.q_on,  row.note_on  = res, q, note

        rows.append(row)
        print()

    # ── Comparison table ──────────────────────────────────────────────────────
    print()
    hr()
    print(c(BOLD, "  Results Summary"))
    hr()
    print()

    col_w = 18
    h1 = f"  {'Prompt':<{col_w}}  {'Quality':^11}  {'TTFT (ms)':^13}  {'TPS':^13}  {'Thinking block'}"
    h2 = f"  {'':<{col_w}}  {'off':>4} {'on':>4}   {'off':>5} {'on':>5}   {'off':>5} {'on':>5}"
    print(c(BOLD, h1))
    print(c(DIM,  h2))
    print(c(DIM, "  " + "─" * 72))

    total_q_off = total_q_on = 0
    total_ttft_off = total_ttft_on = 0.0
    total_tps_off  = total_tps_on  = 0.0
    valid = 0

    for row in rows:
        if row.off.error or row.on.error:
            continue

        tps_off = tps(row.off)
        tps_on  = tps(row.on)
        q_delta = row.q_on - row.q_off

        q_on_col  = c(GREEN,  str(row.q_on))  if row.q_on  > row.q_off else \
                    c(YELLOW, str(row.q_on))   if row.q_on  == row.q_off else \
                    c(RED,    str(row.q_on))
        q_off_col = str(row.q_off)

        delta_q   = c(GREEN, f"+{q_delta}") if q_delta > 0 else \
                    c(DIM,    "=0")          if q_delta == 0 else \
                    c(RED,    str(q_delta))

        ttft_delta = row.on.ttft_ms - row.off.ttft_ms
        ttft_col   = c(RED,    f"+{ttft_delta:.0f}") if ttft_delta > 50  else \
                     c(YELLOW, f"+{ttft_delta:.0f}") if ttft_delta > 10  else \
                     c(GREEN,  f"{ttft_delta:+.0f}")

        think_info = thinking_summary(row.on)

        label_str = row.label[:col_w]
        print(
            f"  {label_str:<{col_w}}  "
            f"{q_off_col:>4} {q_on_col:>4}  {delta_q:>4}   "
            f"{row.off.ttft_ms:>5.0f} {row.on.ttft_ms:>5.0f}  {ttft_col:>6}   "
            f"{tps_off:>5.1f} {tps_on:>5.1f}   "
            f"{think_info}"
        )

        total_q_off   += row.q_off;       total_q_on  += row.q_on
        total_ttft_off += row.off.ttft_ms; total_ttft_on += row.on.ttft_ms
        total_tps_off  += tps_off;         total_tps_on  += tps_on
        valid += 1

    if valid == 0:
        print(c(RED, "  No valid results."))
        return

    print(c(DIM, "  " + "─" * 72))

    avg_q_off   = total_q_off   / valid
    avg_q_on    = total_q_on    / valid
    avg_ttft_off = total_ttft_off / valid
    avg_ttft_on  = total_ttft_on  / valid
    avg_tps_off  = total_tps_off  / valid
    avg_tps_on   = total_tps_on   / valid

    q_winner    = c(GREEN, "thinking") if avg_q_on > avg_q_off else \
                  c(DIM,   "neither")  if avg_q_on == avg_q_off else \
                  c(RED,   "no-think")
    ttft_note   = f"{avg_ttft_on - avg_ttft_off:+.0f} ms overhead"
    tps_note    = f"{avg_tps_on - avg_tps_off:+.1f} tok/s"

    print(
        f"\n  {'Averages':<{col_w}}  "
        f"{avg_q_off:>4.1f} {avg_q_on:>4.1f}  {'':>4}   "
        f"{avg_ttft_off:>5.0f} {avg_ttft_on:>5.0f}  {'':>6}   "
        f"{avg_tps_off:>5.1f} {avg_tps_on:>5.1f}"
    )

    print()
    hr()
    print(c(BOLD, "  Verdict"))
    hr()
    print(f"  Quality winner  : {q_winner}  ({avg_q_off:.2f} → {avg_q_on:.2f} / 3.00)")
    print(f"  TTFT overhead   : {ttft_note}  (avg {avg_ttft_off:.0f} ms → {avg_ttft_on:.0f} ms)")
    print(f"  TPS delta       : {tps_note}  (avg {avg_tps_off:.1f} → {avg_tps_on:.1f} tok/s)")
    print()

    # Per-prompt quality detail
    print(c(BOLD, "  Quality detail"))
    print(c(DIM,  "  (off-score → on-score | scoring note)"))
    print()
    for row in rows:
        delta = row.q_on - row.q_off
        arrow = c(GREEN, "↑") if delta > 0 else c(DIM, "=") if delta == 0 else c(RED, "↓")
        bar_off = quality_bar(row.q_off)
        bar_on  = quality_bar(row.q_on)
        print(f"  {row.label:<{col_w}}  {bar_off} → {bar_on}  {arrow}  off: {row.note_off}  |  on: {row.note_on}")

    print()
    hr("═")
    print()


if __name__ == "__main__":
    main()
