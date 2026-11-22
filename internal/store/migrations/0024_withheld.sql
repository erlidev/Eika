-- Text that ingest removed from a tool result before the model saw it, with
-- where it came from and the screen's score. Like audit it holds no foreign
-- keys, so a row outlives the session it names; call_key is the tool call's
-- key, which is how a tool card is matched to its removals.
CREATE TABLE withheld (
    id          text PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    source_kind text NOT NULL,
    source_name text NOT NULL DEFAULT '',
    session_id  text NOT NULL DEFAULT '',
    run_id      text NOT NULL DEFAULT '',
    call_key    text NOT NULL DEFAULT '',
    content_id  text NOT NULL DEFAULT '',
    text        text NOT NULL,
    score       double precision NOT NULL
);
CREATE INDEX withheld_at_idx ON withheld (at DESC, id DESC);
CREATE INDEX withheld_session_idx ON withheld (session_id, call_key);
