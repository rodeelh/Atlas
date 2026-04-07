#!/usr/bin/env python3
"""Kokoro TTS HTTP server for Atlas.

Loads the Kokoro ONNX model once at startup, then serves POST /synthesize
requests that stream raw little-endian 16-bit mono PCM in the response body.
The Go-side voice.Manager reads the stream chunk-by-chunk and forwards it
to the browser as raw PCM SSE events (same format as Piper).

Endpoints:
  GET  /health       — returns {"ok": true, "voices": [...]}
  POST /synthesize   — JSON body {text, voice, speed, lang}; response body
                       is raw PCM at 24000 Hz, streamed progressively as each
                       sentence finishes synthesis.

The server only listens on 127.0.0.1 and is launched + killed by the Atlas
runtime — never exposed externally.
"""
import argparse
import json
import sys
import traceback
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

parser = argparse.ArgumentParser()
parser.add_argument("--host", default="127.0.0.1")
parser.add_argument("--port", type=int, required=True)
parser.add_argument("--model", required=True, help="Path to kokoro-v1.0.onnx")
parser.add_argument("--voices", required=True, help="Path to voices-v1.0.bin")
args = parser.parse_args()

print(f"kokoro: loading {args.model}", file=sys.stderr, flush=True)

try:
    import numpy as np
    from kokoro_onnx import Kokoro
except Exception as e:
    print(f"kokoro: import failed: {e}", file=sys.stderr, flush=True)
    sys.exit(2)

try:
    kokoro = Kokoro(args.model, args.voices)
except Exception as e:
    print(f"kokoro: model load failed: {e}", file=sys.stderr, flush=True)
    sys.exit(3)

# kokoro_onnx exposes the voice list via the .voices attribute (dict-like).
try:
    voice_list = sorted(list(kokoro.voices.keys()))
except Exception:
    voice_list = []

print(f"kokoro: ready ({len(voice_list)} voices)", file=sys.stderr, flush=True)


def pcm16_bytes(samples) -> bytes:
    """Convert a numpy float32 waveform in [-1, 1] to little-endian int16 bytes."""
    clipped = np.clip(samples, -1.0, 1.0)
    return (clipped * 32767.0).astype("<i2").tobytes()


import re

# Simple end-of-sentence segmenter. Keeps punctuation attached, drops
# blank fragments, falls back to the whole input if there's no sentence
# boundary at all.
_SENTENCE_RE = re.compile(r"[^.!?\n]+[.!?]+|\S[^.!?\n]*\S?$")


def split_sentences(text: str):
    text = text.replace("\r\n", "\n")
    out = []
    for line in text.split("\n"):
        line = line.strip()
        if not line:
            continue
        matches = _SENTENCE_RE.findall(line)
        if not matches:
            out.append(line)
            continue
        for m in matches:
            m = m.strip()
            if m:
                out.append(m)
    return out


class Handler(BaseHTTPRequestHandler):
    # Silence stdout access logging; Atlas reads stderr for diagnostics.
    def log_message(self, fmt, *a):
        pass

    def do_GET(self):
        if self.path == "/health":
            body = json.dumps({"ok": True, "voices": voice_list}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_error(404)

    def do_POST(self):
        if self.path != "/synthesize":
            self.send_error(404)
            return

        try:
            length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(length).decode("utf-8"))
        except Exception as e:
            self._json_error(400, f"invalid body: {e}")
            return

        text = (body.get("text") or "").strip()
        if not text:
            self._json_error(400, "empty text")
            return

        voice = body.get("voice") or "af_heart"
        try:
            speed = float(body.get("speed", 1.0))
        except Exception:
            speed = 1.0
        lang = body.get("lang") or "en-us"

        # HTTP streaming: no Content-Length, close the socket at the end.
        # The Go client reads until EOF.
        self.send_response(200)
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("X-Sample-Rate", "24000")
        self.send_header("X-Voice", voice)
        self.send_header("Cache-Control", "no-store")
        self.end_headers()

        # Per-sentence loop: call kokoro.create() once per sentence and flush
        # PCM as soon as it's ready. Avoids the create_stream() event-loop
        # buffering that delays time-to-first-audio. The Go side already
        # receives one sentence at a time over /voice/synthesize anyway, so
        # there's no behavioral change — just a tighter latency floor.
        try:
            sentences = split_sentences(text)
            for sentence in sentences:
                if not sentence.strip():
                    continue
                samples, sr = kokoro.create(sentence, voice=voice, speed=speed, lang=lang)
                try:
                    self.wfile.write(pcm16_bytes(samples))
                    self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError):
                    return
        except Exception as e:
            print(f"kokoro: synthesize error: {e}", file=sys.stderr, flush=True)
            traceback.print_exc(file=sys.stderr)

    def _json_error(self, status: int, msg: str):
        body = json.dumps({"error": msg}).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


httpd = ThreadingHTTPServer((args.host, args.port), Handler)
print(f"kokoro: listening on http://{args.host}:{args.port}", file=sys.stderr, flush=True)
try:
    httpd.serve_forever()
except KeyboardInterrupt:
    pass
