# Responses API fixtures

Streams of the Responses API, one folder per backend. `openai/responses_test.go`
(in `internal/provider/openai`) plays them through the Responses provider.

| Folder | Source |
|---|---|
| `llamacpp/` | **Recorded** from llama.cpp b11942-338fce651 serving Qwen3.8-27B (reasoning on) on 2026-10-31, nothing edited. `tool_call_request.json` and `answer_request.json` are what the provider sends (key order aside); `tool_call.sse` is the reply to the first (a reasoning item and a function call) and `answer.sse` the reply to the second, which sends the first reply's reasoning item back as returned and the tool result. `overflow.json` is the answer to a prompt of 140053 tokens against a 131072-token slot: an HTTP 400, not an event. llama.cpp reports `encrypted_content` as `""`, puts `timings` beside `response` in `response.completed`, and reports a response cut off by `max_output_tokens` as `completed` (`../usage/llamacpp/responses_cut_in_reasoning.sse`). |
| `openai/` | **Unverified against a live server.** Written from the OpenAI API reference for streaming responses: a reasoning item whose `encrypted_content` is returned (the request set `include: ["reasoning.encrypted_content"]` and `store: false`), a function call, `response.incomplete` with `incomplete_details.reason` `max_output_tokens`, `response.failed`, and the `error` event with `context_length_exceeded`. The encrypted blob is invented. |
| `vllm/` | **Unverified against a live server.** Written from vLLM's `entrypoints/openai/responses` source: a reasoning item with `content` and a null `encrypted_content`, a function call, and the usage block with its per-turn arrays. |

SGLang has a `/v1/responses` route that the project describes as experimental,
and TabbyAPI has none (it would answer 404, which the conformance check reads as
a failure and the provider stays on Chat Completions). Neither has a fixture.

A fixture written from documentation is a claim about what the project sends,
not a recording of it. Replace it with a recording when a server of that kind
is on the bench, and say so in the PLAN log.
