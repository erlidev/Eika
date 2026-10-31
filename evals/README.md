# Evals and live tests

Scripted scenarios against real models, run by `make live`. They are never part
of `make check`. The bench they run on (which models, where) is in
`docs/PLAN.md`, "Live test bench".

```
evals/live/     Go package: env loading, server description (the harness's provider probe), scenarios, report
evals/models/   how to start the bench's models (README.md, guard.py)
evals/results/  one JSON report per run; ignored by git
evals/forward.sh  opens the SSH forward to the chat model when it is closed
```

## Running

Put the bench's variables in `~/.config/eika/live.env` (mode 600), or export
them; an exported variable wins over the file.

```
EIKA_LIVE_ENDPOINT=http://127.0.0.1:8091    # llama.cpp, without /v1
EIKA_LIVE_KEY=...                           # its bearer key
```

Then `make live`. It runs `evals/forward.sh`, which opens the SSH forward from
`evals/models/README.md` when the endpoint is on loopback and its port does not
answer, then `go test -tags live -count=1 -v` on `evals/live`. The test:

- skips, saying why, when `EIKA_LIVE_ENDPOINT` is unset;
- fails, pointing at `evals/models/README.md`, when it is set but does not
  answer, or refuses the key: a skipped live run would look like a pass;
- otherwise plays every scenario, logs one line per model call, and writes the
  report. The path is the last log line.

The scenarios: `provider_probe` (the probe against `/slots`), `cache_reuse`
(a two-turn conversation: cache hit rate and prefill), `tools_and_reasoning`
(tool definitions, a tool call and result, replayed reasoning and the thinking
switch: every prediction must be exact with no drift), `image_input` (one small
picture: never called exact, and counted to the token once its size was
measured), `count_cost` (a context of about 20k tokens counted cold and one
exchange later: `count_seconds`), `agent_loop` (the real agent loop on one
session, two turns with a tool: the call records of the loop must show exact
predictions with no drift), `reasoning_count` (a response cut off inside its
reasoning: the loop's tokenised count, labelled as such, within a few tokens of
the completion tokens the server generated) and `context_breakdown` (the parts
of a plain request counted one by one, plus the template's remainder, equal the
server's prompt count, and the remainder is small and not negative).
`tool_arguments` (a history whose tool calls have spaced, reordered, nested and
non-ASCII arguments), `thinking_switches` (the same short request with every way
a model's thinking is asked for or turned off) and `cut_off_reasoning` (the
request after a response cut off inside its reasoning) count requests a server
may render differently from how they were read: each must be exact with no
drift. The ones that run under both protocols exist twice, the second named
`responses_<name>` (`OnResponses`): the same requests through the Responses
provider, counted from the chat body, which fails if the server's conversion of
the input left out or changed anything the chat request holds. `responses_drift`
is a three-turn conversation with thinking on, parallel tool calls and replayed
reasoning items under the Responses API, and `responses_conformance` runs the
harness's Responses conformance check for real (two small model requests) and
requires it to pass. `session_title` runs the title task of the fast class
against the bench's model, thinking off, and requires a short single-line title.
`queue_priority` puts the inference queue on a client-side pool of one slot
over real model calls: a call holds the slot, a background call and then an
interactive one line up, and the interactive call must be admitted and answered
first (`queue_seconds` in the report is each call's wait for its lease).
`slot_hint_honoured` (and `responses_slot_hint_honoured`) asks the server to run
requests on slot 1, 1, 0, 0 and on a slot it does not have, reads `/slots` while
each runs, and requires the slot named to be the only one processing it (and the
unknown one not to fail the call); the zero drift on those requests shows the hint
is not part of what the counter renders. `slot_affinity` (and
`responses_slot_affinity`) plays two sessions that share a 3000-token head through
the real agent loop on the queue's slot affinity, then the same pair with no hint,
and reports what affinity saved in prefilled tokens in the result's `notes`. It
runs the harness's slot affinity check first. When the check passes, the sessions
must keep one slot each and find their previous prompt cached; when it fails,
hints must not have beaten the control, so the harness is right not to send them.
`shared_admission` is the one scenario that sends large prompts: two sessions
of about 66k tokens each, which together pass the bench's 131072-token cache,
ask at once through a pool that states `kv: shared` with that size (the probe
reads `unknown` for the bench), and must be admitted one after the other with
neither failing or cut off. It costs two cold prefills of that size, about
45 seconds on the bench.

Keep scenarios small: bounded `max_tokens`, no load. The server is shared; do
not start, stop or swap anything on it from a scenario.

## Reading a report

`evals/results/<time>-<build>.json`:

| Field | Meaning |
|---|---|
| `server.build`, `model`, `model_file`, `model_revision` | What answered: the llama.cpp build, the model name requests carry, its weights file and the Hugging Face revision that file came from |
| `server.backend` | What the harness's provider probe took the server for (`llamacpp` on the bench) |
| `server.flags` | What the probe read: `slots`, `context` (the window one request may use; `/props` reports it per slot), `train_context`, `kv` (`unknown` for a llama.cpp server with several slots), `vision`. The KV cache type and `--kv-unified` are not reported by llama.cpp; they are in `command.txt` on the server |
| `scenarios[].passed`, `error` | Whether the scenario ran to its end and met its assertions |
| `calls[].prompt_tokens`, `completion_tokens` | The prompt size the server counted, cached tokens included, and the tokens generated, reasoning included |
| `calls[].reasoning_tokens` | The part of the completion the server says was reasoning; `null` on llama.cpp, which does not say |
| `calls[].prefilled_tokens` | Prompt tokens the server says it evaluated (llama.cpp `prompt_n`) |
| `calls[].cached_tokens`, `cache_hit_rate` | Prompt tokens served from the cache, as the server states them, and their share of the prompt |
| `calls[].prefill_seconds` | Time the server spent evaluating the prompt, as it measured it |
| `calls[].wall_seconds` | Request to end of stream, as the harness saw it |
| `calls[].predicted_tokens`, `prediction_method` | What the token counter (`internal/provider/counter`) predicted for the prompt before the call, and how: `exact`, `anchored` or `estimated` |
| `calls[].drift` | The prediction less `prompt_tokens`. Any drift on an `exact` prediction fails the scenario |
| `calls[].count_seconds` | What predicting the request cost: the tokenizer requests included |
| `calls[].id_slot` | The slot the request named; absent when it named none |
| `scenarios[].notes` | What a scenario states in words, such as what a control run cost |
| `summary` | The same, totalled over a scenario's calls |

A number that was not measured is `null`, never 0. Each scenario is one session
with a token counter of its own, which predicts every request `Target.Complete`
sends and learns from the measurement. The summary's `drift` is the sum of the
absolute drift, so errors in opposite directions do not cancel. A total is
`null` unless every call stated the count it adds up. The token counts are the ones usage parsing reads
(`provider.Usage`); the suite derives none. Compare two reports by their
`server` block first: a change there explains a change in the numbers.

## Adding a scenario

1. Write a function in `evals/live` that returns a `Scenario` (see
   `cachereuse.go`). Its `Run` sends requests with `Target.Complete`, which goes
   through `internal/provider/openai`, and returns the `Call` of each, plus an
   error when an assertion fails. Return the calls made so far with the error:
   the report keeps them.
2. Add it to `Scenarios()` in `scenario.go`.
3. Test it in a file without the `live` tag against a fake server
   (`scenario_test.go` has one that speaks llama.cpp's stream), so `make check`
   covers its logic. A fixture recorded from a real server goes in
   `testdata/`, scrubbed of keys and of anything host-specific. The server's own
   descriptions (`/props`, `/slots`, `/v1/models`) are the probe's fixtures in
   `internal/provider/probe/probetest/testdata/llamacpp/`, which `benchServer` in
   the tests serves.
4. Run `make live`.
