# guard

The injection screen as a service (`docs/design/sentry.md`, section 6). It scores
text with `Horizon-Labs/prompt-injection-guard-small` in bf16 and does the chunking
itself, since it owns the tokenizer.

- `POST /screen {"texts": [...]}`: per text, its windows with byte offsets and the
  score of INJECTION. Windows are 512 tokens, no overlap, scored in batches of 64.
- `POST /predict`: text-embeddings-inference's wire shape, one window per input.
- `GET /health`: `{"status", "model": "<name>@<revision>", "device"}`.

The image (`Dockerfile`) installs CPU `torch` and bakes the model in at the pinned
`REVISION`, so the container needs no network. For a GPU, build with
`--build-arg TORCH_INDEX=https://download.pytorch.org/whl/cu128`. In compose it is
the `guard` service on the `guard` network, which only the harness joins;
`EIKA_GUARD_URL` points the harness elsewhere.

Test: `docker build guard` runs `test_guard.py` against the baked model. To run it
by hand, install `requirements.txt`, download the model into `/model` and run
`python -m unittest` here.
