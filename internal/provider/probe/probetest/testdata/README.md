# Probe fixtures

The responses the probe reads, one folder per backend.

| Folder | Source |
|---|---|
| `llamacpp/` | **Recorded** from llama.cpp b11942-338fce651 serving Qwen3.8-27B with 2 slots on 2026-10-31. `props.json` and `slots.json` leave out `chat_template` and the per-slot `params` objects, which are large and which the probe does not read; everything else is as the server sent it. `apply_template.json` and `tokenize.json` answer `{"messages":[{"role":"user","content":"hi"}]}` and `{"content":"hi"}`. |
| `vllm/` | **Unverified against a live server.** Written from vLLM's documentation and source (`/version`, `/v1/models` with `max_model_len`, the `vllm:cache_config_info` series of `/metrics`, `POST /tokenize`). |
| `sglang/` | **Unverified against a live server.** Written from SGLang's documentation and source (`/get_server_info`, `POST /tokenize`). |
| `tabby/` | **Unverified against a live server.** Written from TabbyAPI's documentation and source (`/v1/model`, `POST /v1/token/encode`). |
| `cloud/` | **Unverified against a live server.** The shape of an OpenAI-style `/v1/models` answer. |

A fixture written from documentation is a claim about what the project sends,
not a recording of it. Replace it with a recording when a server of that kind
is on the bench, and say so in the PLAN log.
