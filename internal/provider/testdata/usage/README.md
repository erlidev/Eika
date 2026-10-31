# Usage fixtures

What each backend states about the tokens a call cost, one folder per backend.
`usage_test.go` reads them to pin which field means what and which counts are
NULL when a backend does not state them.

| Folder | Source |
|---|---|
| `llamacpp/` | **Recorded** from llama.cpp b11942-338fce651 serving Qwen3.8-27B (reasoning on, 2 slots) on 2026-10-31, streamed, nothing edited. `chat_*` is `POST /v1/chat/completions`, `responses_*` is `POST /v1/responses`; the matching `*_request.json` is what was sent (the short ones set `max_completion_tokens` / `max_output_tokens` to 20). `*_cold` is the first request after a changed system prompt, so nothing is cached; `*_warm` repeats it, so the prompt is cached up to its last tokens. `*_cut_in_reasoning` is the warm request cut off at 20 tokens, all of them reasoning. |
| `openai/` | **Unverified against a live server.** Written from the OpenAI API reference: `usage.prompt_tokens_details.cached_tokens`, `usage.completion_tokens_details.reasoning_tokens`, and under Responses `input_tokens_details.cached_tokens` and `output_tokens_details.reasoning_tokens`. `chat_early_chunk.json` is the `"usage": null` that every chunk before the last carries. |
| `vllm/` | **Unverified against a live server.** Written from vLLM's documentation and source: `prompt_tokens_details` is present only with `--enable-prompt-tokens-details` and is `null` otherwise; the usage block has no reasoning count on Chat Completions. |
| `sglang/` | **Unverified against a live server.** Written from SGLang's source: `prompt_tokens_details` is present only with `--enable-cache-report`, and the usage block has a top-level `reasoning_tokens` that defaults to 0 whether or not anything counted reasoning, which is why it is not read. |
| `tabby/` | **Unverified against a live server.** Written from TabbyAPI's source: the usage block adds timings and rates and has no cached or reasoning count. |

A fixture written from documentation is a claim about what the project sends,
not a recording of it. Replace it with a recording when a server of that kind
is on the bench, and say so in the PLAN log.
