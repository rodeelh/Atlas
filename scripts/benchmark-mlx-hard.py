#!/usr/bin/env python3
"""
benchmark-mlx-hard.py
──────────────────────────────────────────────────────────────────────────────
Hard benchmark: multi-step proofs, complex code, and ambiguous edge cases.
Runs 10 prompts twice each — thinking=off then thinking=on — and scores on
answer quality, TTFT, and output TPS (thinking tokens excluded from TPS).

  Quality   0–3   all required keywords = 2 pts, any bonus keyword = +1 pt
  Latency   ms    time to first output token (after thinking phase ends)
  TPS       tok/s output tokens / generation time (thinking time excluded)

Usage:
  python3 scripts/benchmark-mlx-hard.py [--port 11990] [--max-tokens 2000]
──────────────────────────────────────────────────────────────────────────────
"""

import argparse
import json
import os
import re
import sys
import time
import urllib.request
from dataclasses import dataclass
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

# ── Hard prompts ───────────────────────────────────────────────────────────────
# Each entry: (label, prompt, required_keywords, bonus_keywords)
#   required_keywords — ALL must appear (case-insensitive) for 2 pts
#   bonus_keywords    — ANY match adds +1 reasoning bonus
PROMPTS = [
    (
        "√2 irrationality proof",
        (
            "Prove that √2 is irrational. Write a complete, rigorous proof "
            "and explain each step."
        ),
        ["contradiction", "even"],
        ["assume", "p/q", "coprime", "rational", "both even", "common factor"],
    ),
    (
        "Monty Hall problem",
        (
            "You are on a game show. There are 3 doors: one hides a car, two hide goats. "
            "You pick door 1. The host (who always knows what is behind each door) opens "
            "door 3, revealing a goat. He then offers you the chance to switch to door 2. "
            "Should you switch? What is the exact probability of winning if you switch? "
            "Show your reasoning."
        ),
        ["2/3", "switch"],
        ["1/3", "conditional", "bayes", "probability", "prior", "posterior"],
    ),
    (
        "Proof by induction",
        (
            "Prove by mathematical induction that for all positive integers n:\n"
            "1 + 2 + 3 + … + n = n(n+1)/2\n"
            "Be explicit about the base case and the inductive step."
        ),
        ["n(n+1)/2", "induction"],
        ["base case", "inductive step", "k+1", "assume", "hypothesis"],
    ),
    (
        "Pipes rate problem",
        (
            "Pipe A can fill an empty tank in 4 hours. "
            "Pipe B can drain a full tank in 6 hours. "
            "If the tank starts empty and both pipes are open at the same time, "
            "how many hours does it take to fill the tank? Show your work."
        ),
        ["12"],   # net rate 1/4 - 1/6 = 1/12 → 12 hours
        ["1/4", "1/6", "1/12", "rate", "combined", "net"],
    ),
    (
        "Bertrand's Box paradox",
        (
            "There are three boxes:\n"
            "  Box A: two gold coins\n"
            "  Box B: two silver coins\n"
            "  Box C: one gold coin and one silver coin\n"
            "You pick a random box and draw one coin at random — it is gold. "
            "What is the probability that the other coin in the same box is also gold? "
            "Show your reasoning carefully."
        ),
        ["2/3"],   # Bertrand's box — most people intuit 1/2, answer is 2/3
        ["conditional", "bayes", "not 1/2", "gold coin", "three gold", "two gold"],
    ),
    (
        "Binary search bug hunt",
        (
            "Find ALL bugs in the following Python function and provide a corrected version:\n\n"
            "```python\n"
            "def binary_search(arr, target):\n"
            "    left, right = 0, len(arr)\n"
            "    while left < right:\n"
            "        mid = (left + right) // 2\n"
            "        if arr[mid] == target:\n"
            "            return mid\n"
            "        elif arr[mid] < target:\n"
            "            left = mid\n"
            "        else:\n"
            "            right = mid - 1\n"
            "    return -1\n"
            "```\n\n"
            "Explain why each bug causes incorrect behaviour."
        ),
        # Bug 1: right = len(arr) → off-by-one (should be len(arr)-1)
        # Bug 2: left = mid → infinite loop when arr[mid] < target (should be mid+1)
        # Bug 3: while left < right → misses target when left==right (should be <=)
        ["mid + 1", "len(arr) - 1"],
        ["off by one", "infinite loop", "left <= right", "boundary", "off-by-one"],
    ),
    (
        "Birthday paradox",
        (
            "What is the minimum number of people in a room so that the probability "
            "of at least two people sharing a birthday exceeds 50%? "
            "Explain your calculation step by step. Assume 365 days and equal probability."
        ),
        ["23"],
        ["complement", "364", "365", "probability", "1 -", "at least"],
    ),
    (
        "Russell's paradox",
        (
            "Let R be the set of all sets that do not contain themselves as members. "
            "Does R contain itself? Explain the logical implications of each possibility "
            "and what this reveals about naive set theory."
        ),
        ["paradox", "contradiction"],
        ["contain itself", "does not contain", "russell", "neither", "both", "naive set theory"],
    ),
    (
        "LRU cache implementation",
        (
            "Implement an LRU (Least Recently Used) cache in Python with O(1) time "
            "complexity for both get and put operations. "
            "Include the full implementation, explain your data structure choices, "
            "and demonstrate it with a short usage example."
        ),
        ["o(1)", "get", "put"],
        ["ordereddict", "doubly linked", "hashmap", "hash map", "deque", "capacity"],
    ),
    (
        "Knights and Knaves",
        (
            "On an island, knights always tell the truth and knaves always lie.\n"
            "  A says: 'B is a knave.'\n"
            "  B says: 'A and C are both knights.'\n"
            "  C says: 'A is a knave.'\n"
            "Determine whether each of A, B, and C is a knight or a knave. "
            "Show your reasoning for each case."
        ),
        # Solution: A=knight, B=knave, C=knave
        # A(knight) says B is knave → B is knave (true ✓)
        # B(knave) says A and C are both knights → false, so NOT(A knight AND C knight)
        #   Since A is knight, C must be knave ✓
        # C(knave) says A is knave → false, so A is knight ✓
        ["b is a knave", "c is a knave"],
        ["a is a knight", "assume", "consistent", "contradiction", "knight", "knave"],
    ),
]

# ── Streaming helper ──────────────────────────────────────────────────────────

@dataclass
class StreamResult:
    text: str = ""
    thinking: str = ""
    ttft_ms: float = 0.0
    gen_ms: float = 0.0
    output_tokens: int = 0
    prompt_tokens: int = 0
    error: Optional[str] = None


def stream_completion(url: str, model: str, prompt: str,
                      thinking: bool, max_tokens: int) -> StreamResult:
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

    # mlx-lm splits output into two SSE delta fields:
    #   delta.reasoning — tokens while in the thinking/reasoning state
    #   delta.content   — tokens in normal output state
    # <think> tags never appear in delta.content.

    first_output_seen = False
    t_thinking_end: Optional[float] = None

    try:
        with urllib.request.urlopen(req, timeout=600) as resp:
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

                if "usage" in chunk and "choices" not in chunk:
                    result.prompt_tokens = chunk["usage"].get("prompt_tokens", 0)
                    result.output_tokens = chunk["usage"].get("completion_tokens", 0)
                    continue

                choices = chunk.get("choices", [])
                if not choices:
                    continue

                delta     = choices[0].get("delta", {})
                reasoning = delta.get("reasoning") or ""
                content   = delta.get("content") or ""
                now       = time.perf_counter()

                if reasoning:
                    result.thinking += reasoning
                    t_thinking_end = now

                if content:
                    if not first_output_seen:
                        first_output_seen = True
                        result.ttft_ms = (now - t_request) * 1000
                    result.text += content

    except Exception as exc:
        result.error = str(exc)
        return result

    t_end = time.perf_counter()

    if first_output_seen:
        output_start = t_thinking_end if t_thinking_end else t_request
        result.gen_ms = max((t_end - output_start) * 1000, 1)
    else:
        result.gen_ms = 1

    return result


# ── Quality scoring ───────────────────────────────────────────────────────────

def score_quality(text: str, required: list, bonus: list):
    lower = (text + " " + text).lower()   # double so phrases crossing word boundaries match

    matched_req = [kw for kw in required if kw.lower() in lower]
    matched_bon = [kw for kw in bonus    if kw.lower() in lower]

    if len(matched_req) == len(required) and len(required) > 0:
        score = 3 if matched_bon else 2
        note  = f"✓ answer ({'+reasoning' if matched_bon else 'no reasoning bonus'})"
    elif matched_req:
        score = 1
        note  = f"~ partial ({len(matched_req)}/{len(required)} required keywords)"
    else:
        score = 0
        note  = "✗ wrong/missing"

    return score, note


# ── Display helpers ───────────────────────────────────────────────────────────

def tps(result: StreamResult) -> float:
    if not result.output_tokens or result.gen_ms <= 0:
        return 0.0
    return result.output_tokens / (result.gen_ms / 1000)


def bar(score: int) -> str:
    filled = "█" * score
    empty  = "░" * (3 - score)
    colour = GREEN if score == 3 else YELLOW if score >= 1 else RED
    return c(colour, filled) + c(DIM, empty)


def thinking_summary(result: StreamResult) -> str:
    if not result.thinking:
        return c(DIM, "none")
    words = len(result.thinking.split())
    return f"{words} words"


def delta_colour(val: float, higher_is_better: bool = True) -> str:
    if val == 0:
        return c(DIM, f"{val:+.0f}")
    good = (val > 0) == higher_is_better
    colour = GREEN if good else RED
    return c(colour, f"{val:+.0f}")


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--atlas-url", default="http://localhost:1984")
    parser.add_argument("--port", type=int, default=None,
                        help="MLX server port (default: read from Atlas config)")
    parser.add_argument("--max-tokens", type=int, default=2000)
    parser.add_argument("--prompts", type=str, default=None,
                        help="Comma-separated 1-based indices, e.g. '1,3,5'")
    args = parser.parse_args()

    # Resolve config from Atlas
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

    model_base = os.path.basename(model_name.rstrip("/"))
    mlx_port   = args.port or cfg.get("atlasMLXPort", 11990)
    mlx_url    = f"http://127.0.0.1:{mlx_port}"

    try:
        with urllib.request.urlopen(f"{mlx_url}/health", timeout=5):
            pass
    except Exception:
        print(c(RED, f"MLX server not responding at {mlx_url}."))
        sys.exit(1)

    # Resolve the actual model ID from /v1/models (server may use full path)
    try:
        with urllib.request.urlopen(f"{mlx_url}/v1/models", timeout=5) as r:
            models_resp = json.load(r)
        ids = [m["id"] for m in models_resp.get("data", [])]
        resolved = next(
            (mid for mid in ids
             if mid == model_base or os.path.basename(mid.rstrip("/")) == model_base),
            ids[0] if ids else model_base,
        )
        model_id = resolved
    except Exception:
        model_id = model_base

    # Prompt subset
    selected = list(range(len(PROMPTS)))
    if args.prompts:
        try:
            selected = [int(i) - 1 for i in args.prompts.split(",")]
        except ValueError:
            print(c(RED, "Invalid --prompts value."))
            sys.exit(1)

    prompts_to_run = [PROMPTS[i] for i in selected]

    print()
    print(c(BOLD, "═" * 72))
    print(c(BOLD, "  MLX Hard Benchmark — proofs · code · ambiguous edge cases"))
    print(f"  Model : {model_base}   Port : {mlx_port}   Max tokens : {args.max_tokens}")
    print(c(BOLD, "═" * 72))
    print()

    # ── Run prompts ───────────────────────────────────────────────────────────
    rows = []
    for idx, (label, prompt, required, bonus) in enumerate(prompts_to_run, 1):
        results = {}
        for thinking in (False, True):
            tag = c(CYAN, "thinking=on ") if thinking else c(DIM, "thinking=off")
            print(f"  [{idx}/{len(prompts_to_run)}] {c(BOLD, label):<40} {tag} … ", end="", flush=True)
            r = stream_completion(mlx_url, model_id, prompt, thinking, args.max_tokens)
            if r.error:
                print(c(RED, f"ERROR: {r.error}"))
            else:
                score, note = score_quality(r.text, required, bonus)
                t = tps(r)
                think_wc = len(r.thinking.split()) if r.thinking else 0
                print(f"TTFT {r.ttft_ms:>6.0f}ms  {t:>5.1f} tok/s  Q={score}/3  {note}")
                results[thinking] = (r, score, note, t, think_wc)

        if len(results) == 2:
            rows.append((label, results[False], results[True], required, bonus))
        print()

    if not rows:
        return

    # ── Summary table ─────────────────────────────────────────────────────────
    print(c(BOLD, "─" * 72))
    print(c(BOLD, "  Results Summary"))
    print(c(BOLD, "─" * 72))
    print()
    print(c(BOLD, f"  {'Prompt':<26} {'Quality':^10}  {'TTFT (ms)':^16}  {'TPS':^14}  {'Think words':^12}"))
    print(c(DIM,  f"  {'':26} {'off':>4} {'on':>4}  {'off':>6} {'on':>6}  {'off':>6} {'on':>6}  {'on':>10}"))
    print(c(DIM,  "  " + "─" * 70))

    sum_q_off = sum_q_on = 0
    sum_ttft_off = sum_ttft_on = 0
    sum_tps_off = sum_tps_on = 0
    n = 0

    for label, (r_off, q_off, _, tps_off, _tw_off), (r_on, q_on, _, tps_on, tw_on), _, _ in rows:
        dq   = q_on - q_off
        dttft = r_on.ttft_ms - r_off.ttft_ms
        dtps  = tps_on - tps_off

        dq_str   = (c(GREEN, f"+{dq}") if dq > 0 else c(RED, f"{dq}") if dq < 0 else c(DIM, f"={dq}"))
        dttft_str = delta_colour(-dttft, higher_is_better=True)   # lower TTFT is better
        dtps_str  = delta_colour(dtps,   higher_is_better=True)

        think_str = f"{tw_on} w" if tw_on else c(DIM, "none")

        print(
            f"  {label:<26} "
            f"{q_off:>3} {c(YELLOW, str(q_on)):>3}  {dq_str:>4}  "
            f"{r_off.ttft_ms:>6.0f} {r_on.ttft_ms:>6.0f}  {dttft_str:>6}  "
            f"{tps_off:>6.1f} {tps_on:>6.1f}  "
            f"{think_str:>12}"
        )

        sum_q_off += q_off; sum_q_on += q_on
        sum_ttft_off += r_off.ttft_ms; sum_ttft_on += r_on.ttft_ms
        sum_tps_off += tps_off; sum_tps_on += tps_on
        n += 1

    avg = lambda s: s / n if n else 0
    print(c(DIM, "  " + "─" * 70))
    print(
        f"  {'Averages':<26} "
        f"{avg(sum_q_off):>3.1f} {avg(sum_q_on):>3.1f}  "
        f"{'':>4}  "
        f"{avg(sum_ttft_off):>6.0f} {avg(sum_ttft_on):>6.0f}  "
        f"{'':>6}  "
        f"{avg(sum_tps_off):>6.1f} {avg(sum_tps_on):>6.1f}"
    )

    # ── Verdict ───────────────────────────────────────────────────────────────
    print()
    print(c(BOLD, "─" * 72))
    print(c(BOLD, "  Verdict"))
    print(c(BOLD, "─" * 72))

    dq_avg   = avg(sum_q_on) - avg(sum_q_off)
    dttft_avg = avg(sum_ttft_on) - avg(sum_ttft_off)
    dtps_avg  = avg(sum_tps_on) - avg(sum_tps_off)

    q_winner = (
        c(CYAN,  "thinking=ON  wins") if dq_avg > 0 else
        c(DIM,   "thinking=OFF wins") if dq_avg < 0 else
        c(DIM,   "neither")
    )
    print(f"  Quality winner  : {q_winner}  ({avg(sum_q_off):.2f} → {avg(sum_q_on):.2f} / 3.00)")
    print(f"  TTFT overhead   : {dttft_avg:+.0f} ms  (avg {avg(sum_ttft_off):.0f} ms → {avg(sum_ttft_on):.0f} ms)")
    print(f"  TPS delta       : {dtps_avg:+.1f} tok/s  (avg {avg(sum_tps_off):.1f} → {avg(sum_tps_on):.1f} tok/s)")

    # ── Per-prompt quality detail ─────────────────────────────────────────────
    print()
    print(c(BOLD, "  Quality detail"))
    print(c(DIM,  "  (off-score → on-score | off answer | on answer)"))
    print()
    for label, (_r_off, q_off, note_off, _, _), (_r_on, q_on, note_on, _, _), _, _ in rows:
        arrow = "▲" if q_on > q_off else "▼" if q_on < q_off else "="
        arrow_c = c(GREEN, arrow) if q_on > q_off else c(RED, arrow) if q_on < q_off else c(DIM, arrow)
        print(f"  {label:<26} {bar(q_off)} → {bar(q_on)}  {arrow_c}  off: {note_off}  |  on: {note_on}")

    print()
    print(c(BOLD, "═" * 72))


if __name__ == "__main__":
    main()
