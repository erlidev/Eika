"""Serve an injection-screen classifier for Eika's live tests.

The model runs as docs/design/sentry.md measured it: transformers, bf16, the
publisher's tokenizer (its normalizer is part of the accuracy). The wire shape
matches text-embeddings-inference's /predict, so a TEI deployment can replace
this script without changing the client.

    python -I guard.py --model <folder> --port 9940

POST /predict {"inputs": "text" | ["text", ...]}
  -> [[{"label": "INJECTION", "score": 0.97}, {"label": "SAFE", "score": 0.03}], ...]
GET /health -> {"status": "ok", "model": "<name>", "device": "cuda"}

Each input is scored as one window, truncated to the model's maximum length;
chunking long content is the caller's job (Sentry's ingest).
"""

import argparse
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Lock

import torch
from transformers import AutoModelForSequenceClassification, AutoTokenizer

MAX_INPUTS = 64


def load(folder: str):
    device = "cuda" if torch.cuda.is_available() else "cpu"
    tokenizer = AutoTokenizer.from_pretrained(folder)
    model = AutoModelForSequenceClassification.from_pretrained(folder, dtype=torch.bfloat16)
    return tokenizer, model.to(device).eval(), device


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=9940)
    args = parser.parse_args()

    tokenizer, model, device = load(args.model)
    labels = model.config.id2label
    name = Path(args.model).name
    lock = Lock()  # one batch on the GPU at a time

    def predict(texts: list[str]) -> list[list[dict]]:
        batch = tokenizer(texts, truncation=True, padding=True, return_tensors="pt").to(device)
        with lock, torch.inference_mode():
            probs = model(**batch).logits.float().softmax(-1).cpu()
        return [
            sorted(({"label": labels[i], "score": round(float(p[i]), 6)} for i in range(len(p))),
                   key=lambda x: -x["score"])
            for p in probs
        ]

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
            if self.path != "/predict":
                self.reply(404, {"error": "not found"})
                return
            try:
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
                inputs = body["inputs"]
                texts = [inputs] if isinstance(inputs, str) else list(inputs)
                if not texts or len(texts) > MAX_INPUTS or not all(isinstance(t, str) for t in texts):
                    raise ValueError(f"inputs must be a string or 1 to {MAX_INPUTS} strings")
            except (ValueError, KeyError, TypeError) as e:
                self.reply(400, {"error": str(e)})
                return
            self.reply(200, predict(texts))

        def log_message(self, format, *args) -> None:  # keep content out of logs
            pass

    print(f"serving {name} on {device} at http://{args.host}:{args.port}", flush=True)
    ThreadingHTTPServer((args.host, args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
