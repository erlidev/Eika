-- What a probe learned about a provider's endpoint, and what the user set in
-- its place (docs/design/runtime.md section 1, "Endpoints").
--
-- backend, slots, kv and kv_tokens hold the value in force. A probe writes
-- them, except those named in overridden, which the user set and the next
-- probe leaves alone. pool is the hardware group; empty means the provider's
-- own name, so renaming a provider moves its default pool with it.
-- kv_tokens is the size of a shared cache in tokens, zero when unknown.
-- capabilities is the last probe's result that is not a setting: how the
-- endpoint counts tokens, whether it reports cache metrics, the window it
-- reported. probed_at is the last probe that succeeded and probe_error why
-- the latest one failed, empty after a success, so a probe that fails at
-- start leaves the earlier result in place.

ALTER TABLE providers
    ADD COLUMN backend      text NOT NULL DEFAULT 'other'
        CHECK (backend IN ('llamacpp', 'vllm', 'sglang', 'tabby', 'openai', 'other')),
    ADD COLUMN pool         text NOT NULL DEFAULT '',
    ADD COLUMN slots        integer NOT NULL DEFAULT 1 CHECK (slots > 0),
    ADD COLUMN kv           text NOT NULL DEFAULT 'unknown'
        CHECK (kv IN ('shared', 'partitioned', 'unknown')),
    ADD COLUMN kv_tokens    integer NOT NULL DEFAULT 0 CHECK (kv_tokens >= 0),
    ADD COLUMN capabilities jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN overridden   text[] NOT NULL DEFAULT '{}',
    ADD COLUMN probed_at    timestamptz,
    ADD COLUMN probe_error  text NOT NULL DEFAULT '';
