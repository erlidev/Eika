"""Serve the injection screen for Eika: the `guard` service.

The model runs as docs/design/sentry.md measured it: transformers, bf16, the
publisher's tokenizer (its normalizer is part of the accuracy).

    python -I guard.py --model <folder> --port 9940

POST /screen {"texts": ["...", ...]}
  -> {"results": [{"windows": [{"start": 0, "end": 1873, "score": 0.02}, ...]}, ...]}
  Each text is cut into windows of 512 tokens, special tokens included, with no
  overlap. start and end are byte offsets into the UTF-8 text; the windows of a
  text tile it. score is the probability of INJECTION. At most 64 texts.
POST /predict {"inputs": "text" | ["text", ...]}
  -> [[{"label": "INJECTION", "score": 0.97}, {"label": "SAFE", "score": 0.03}], ...]
  The wire shape of text-embeddings-inference. Each input is one window,
  truncated to 512 tokens.
GET /health -> {"status": "ok", "model": "<name>@<revision>", "device": "cpu"}
"""

import argparse
import itertools
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Lock

import torch
from transformers import AutoModelForSequenceClassification, AutoTokenizer

MAX_INPUTS = 64  # texts per request, and windows per batch
MAX_TOKENS = 512  # per window, special tokens included


def load(folder: str):
    device = "cuda" if torch.cuda.is_available() else "cpu"
    tokenizer = AutoTokenizer.from_pretrained(folder)
    model = AutoModelForSequenceClassification.from_pretrained(folder, dtype=torch.bfloat16)
    return tokenizer, model.to(device).eval(), device


def split(tokenizer, text: str) -> tuple[list[list[int]], list[tuple[int, int]]]:
    """Cut text into windows of MAX_TOKENS tokens, special tokens included.

    Returns each window's token ids and its byte range in the UTF-8 text. The
    ranges tile the text: a window starts where the one before it ended.
    """
    data = text.encode()  # a lone surrogate raises ValueError here
    enc = tokenizer(text, add_special_tokens=False, return_offsets_mapping=True)
    ids, offsets = enc["input_ids"], enc["offset_mapping"]
    if not ids:
        return [], []
    # Offsets count characters; the wire counts bytes.
    to_byte = [0, *itertools.accumulate(len(c.encode()) for c in text)] if len(data) != len(text) else None
    size = MAX_TOKENS - 2  # [CLS] and [SEP]
    chunks, ranges, start = [], [], 0
    for i in range(0, len(ids), size):
        last = min(i + size, len(ids)) - 1
        end = offsets[last][1]
        end = len(data) if last == len(ids) - 1 else (to_byte[end] if to_byte else end)
        chunks.append([tokenizer.cls_token_id, *ids[i:last + 1], tokenizer.sep_token_id])
        ranges.append((start, end))
        start = end
    return chunks, ranges


def service(folder: str):
    """Load the model; return its screen and predict functions, its name and device."""
    tokenizer, model, device = load(folder)
    tokenizer.model_max_length = 1 << 30  # windows are cut here, not by the tokenizer
    labels = model.config.id2label
    injection = next(i for i, label in labels.items() if label == "INJECTION")
    name_file = Path(folder) / "NAME"  # written by the image build: name@revision
    name = name_file.read_text().strip() if name_file.exists() else Path(folder).name
    lock = Lock()  # one batch at a time

    def probs(input_ids: list[list[int]]) -> list:
        batch = tokenizer.pad({"input_ids": input_ids}, return_tensors="pt").to(device)
        with lock, torch.inference_mode():
            return model(**batch).logits.float().softmax(-1).cpu()

    def predict(texts: list[str]) -> list[list[dict]]:
        enc = tokenizer(texts, truncation=True, max_length=MAX_TOKENS)["input_ids"]
        return [
            sorted(({"label": labels[i], "score": round(float(p[i]), 6)} for i in range(len(p))),
                   key=lambda x: -x["score"])
            for p in probs(enc)
        ]

    def screen(texts: list[str]) -> dict:
        spans, inputs = [], []  # spans[t] holds the byte range of each of text t's windows
        for text in texts:
            chunks, ranges = split(tokenizer, text)
            spans.append(ranges)
            inputs += chunks
        scores = []
        for i in range(0, len(inputs), MAX_INPUTS):
            scores += [round(float(p[injection]), 6) for p in probs(inputs[i:i + MAX_INPUTS])]
        results, k = [], 0
        for ranges in spans:
            results.append({"windows": [
                {"start": s, "end": e, "score": scores[k + j]} for j, (s, e) in enumerate(ranges)
            ]})
            k += len(ranges)
        return {"results": results}

    return screen, predict, name, device


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=9940)
    args = parser.parse_args()

    screen, predict, name, device = service(args.model)

    class Handler(BaseHTTPRequestHandler):
        def reply(self, status: int, body) -> None:
            data = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self) -> None:
            if self.path == "/health":
                self.reply(200, {"status": "ok", "model": name, "device": device})
            else:
                self.reply(404, {"error": "not found"})

        def do_POST(self) -> None:
            if self.path not in ("/predict", "/screen"):
                self.reply(404, {"error": "not found"})
                return
            try:
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
                key = "inputs" if self.path == "/predict" else "texts"
                inputs = body[key]
                texts = [inputs] if isinstance(inputs, str) else list(inputs)
                if not texts or len(texts) > MAX_INPUTS or not all(isinstance(t, str) for t in texts):
                    raise ValueError(f"{key} must be 1 to {MAX_INPUTS} strings")
                reply = predict(texts) if self.path == "/predict" else screen(texts)
            except (ValueError, KeyError, TypeError) as e:
                self.reply(400, {"error": str(e)})
                return
            self.reply(200, reply)

        def log_message(self, format, *args) -> None:  # keep content out of logs
            pass

    print(f"serving {name} on {device} at http://{args.host}:{args.port}", flush=True)
    ThreadingHTTPServer((args.host, args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
