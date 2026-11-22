# Test models

The live test bench (`docs/PLAN.md`, "Live test bench"): real models for
`make live` and the evals. None of them runs in `make check`.

| Role | Model | Where | Endpoint |
|---|---|---|---|
| chat | Qwen3.8-27B UD-Q4_K_XL (5.1 bpw, revision 4af51b35) on llama.cpp b11942-338fce651, text only, MTP, 4 slots sharing a 256k q8_0 KV cache | the RTX 5090 server, Komodo Deployment `llama-server` | `EIKA_LIVE_ENDPOINT` (`https://llama.erli.xyz`, through Caddy), bearer `EIKA_LIVE_KEY` |
| injection screen | `Horizon-Labs/prompt-injection-guard-small`, bf16 | local GPU, `guard.py` here | `EIKA_GUARD_URL`, default `http://127.0.0.1:9940` |
| `decision` | `vllm-sr/d3-flash` Q8_0 on llama.cpp | local GPU, your own llama.cpp build | `EIKA_DECISION_URL`, default `http://127.0.0.1:9941` |

Keep the variables you need in `~/.config/eika/live.env` (mode 600), never in
the repository. The Phase 2 live suite needs the chat endpoint and key, Docker,
and PostgreSQL. It starts a temporary PostgreSQL container unless
`EIKA_TEST_DATABASE_URL` names a test server. The guard and decision models are
not needed for that suite. Run it with `make live`; see `evals/README.md`.

## Injection screen

`guard.py` serves the model the way `docs/design/sentry.md` measured it
(transformers, bf16, the publisher's tokenizer) on text-embeddings-inference's
`/predict` shape. TEI 1.9 itself does not load the model: it reads ModernBERT's
older rope fields and expects one output label where this model has two.

```
hf download Horizon-Labs/prompt-injection-guard-small --local-dir <dir>
python -I evals/models/guard.py --model <dir> --port 9940
```

It needs `torch` and `transformers` 5 in the Python that runs it.

## Decision model

A llama.cpp recent enough to serve `/v1/systemone`:

```
hf download ggml-org/vllm-d3-flash-GGUF vllm-d3-flash-Q8_0.gguf --local-dir <dir>
llama serve -m <dir>/vllm-d3-flash-Q8_0.gguf --alias d3-flash -ngl 99 -fa on \
  -np 2 --kv-unified -c 32768 -b 8192 -ub 8192 -cram 0 --metrics \
  --host 127.0.0.1 --port 9941
```

Two slots share 32k tokens, since the reviewer's state is capped at 16k.

## Keeping them running

On a desktop with systemd, run each as a transient user unit with a memory cap,
so it survives the terminal, stops with `systemctl --user stop`, and on a leak
is killed itself instead of pushing the desktop into swap:

```
systemd-run --user --unit=eika-test-guard \
  -p MemoryHigh=2600M -p MemoryMax=3G -p MemorySwapMax=256M \
  <python> -I <repo>/evals/models/guard.py --model <dir>
systemd-run --user --unit=eika-test-decision \
  -p MemoryHigh=3G -p MemoryMax=4G -p MemorySwapMax=256M \
  llama serve ...
```

The guard needs about 2.5 GB (torch and CUDA). The decision server peaks near
the GGUF's size while loading, then keeps about 0.4 GB of its own: the cap only
makes the kernel drop the file cache sooner. `-cram 0` keeps llama.cpp's host
prompt cache off.

## The chat model

The `llama-server` Deployment (image `llama-server-gpu:0.0.9`, network `proxy`)
serves it at `https://llama.erli.xyz`; Caddy proxies to `llama-server:8080`.
Nothing needs forwarding, so `evals/forward.sh` does nothing for an `https`
endpoint. It has no files in `/opt/stacks`; read its flags with
`docker inspect llama-server`: `-hf unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_XL
--no-mmproj --spec-type draft-mtp --spec-draft-n-max 3 --alias qwen3.8-27b
-ngl 99 -fa on --ctx-size 256000 --cache-type-k q8_0 --cache-type-v q8_0
--batch-size 2048 --ubatch-size 1024 --kv-unified --no-context-shift`
(sampling: temp 0.8, top-p 0.95, top-k 20, min-p 0).

One model fills the 5090 at a time, and ComfyUI also holds GPU memory: stop
`comfyui` and `llama-qwen` before starting `llama-server`.

`llama-qwen` (`/opt/stacks/llama-qwen`: `komodo.toml`, `command.txt`, `run.sh`,
`.env`) is the earlier bench container, 2 slots and 128k, with vision, LAN only
on `10.10.10.2:8091`. The bench runner (`evals/bench`) still names it as its
chat container and restores it; it has not been repointed at `llama-server`.
To use it, put `EIKA_LIVE_ENDPOINT=http://127.0.0.1:8091` in `live.env` and
`make live` opens the forward:

```
ssh -fN -o ControlMaster=auto -o ControlPath=~/.ssh/cm/%r@%h:%p -o ControlPersist=4h \
  -L 127.0.0.1:8091:10.10.10.2:8091 ubuntu-5090
```
