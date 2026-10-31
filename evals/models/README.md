# Test models

The live test bench (`docs/PLAN.md`, "Live test bench"): real models for
`make live` and the evals. None of them runs in `make check`.

| Role | Model | Where | Endpoint |
|---|---|---|---|
| chat | Qwen3.8-27B UD-Q4_K_XL (5.1 bpw) on llama.cpp, 2 slots sharing a 128k q8_0 KV cache | the RTX 5090 server, Komodo Deployment `llama-qwen` (`/opt/stacks/llama-qwen`) | `EIKA_LIVE_ENDPOINT` (`http://127.0.0.1:8091` through SSH), bearer `EIKA_LIVE_KEY` |
| injection screen | `Horizon-Labs/prompt-injection-guard-small`, bf16 | local GPU, `guard.py` here | `EIKA_GUARD_URL`, default `http://127.0.0.1:9940` |
| `decision` | `vllm-sr/d3-flash` Q8_0 on llama.cpp | local GPU, your own llama.cpp build | `EIKA_DECISION_URL`, default `http://127.0.0.1:9941` |

Keep all four variables in `~/.config/eika/live.env` (mode 600), never in the
repository.

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

`/opt/stacks/llama-qwen` on the server holds `komodo.toml` (the Deployment, to
import into Komodo), `command.txt`, `run.sh` (the same container through plain
Docker) and `.env` with the key. It is LAN only for now, on `10.10.10.2:8091`
with no Caddy site; ufw drops that port from other subnets, so forward it:

```
ssh -fN -o ControlMaster=auto -o ControlPath=~/.ssh/cm/%r@%h:%p -o ControlPersist=4h \
  -L 127.0.0.1:8091:10.10.10.2:8091 ubuntu-5090
```

`make live` runs that command itself when the endpoint is on loopback and its
port does not answer. Or allow it once on the server:
`sudo ufw allow from 192.168.0.0/24 to any port 8091 proto tcp`.

One model fills the 5090 at a time: stop `llama-server` and `tabbyapi` first.
