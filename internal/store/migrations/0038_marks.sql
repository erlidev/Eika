-- Marks record, per run, what Sentry saw that a tripwire may later join into a
-- chain (docs/design/sentry.md, section 11). A mark changes nothing by itself.
CREATE TABLE marks (
    id text PRIMARY KEY,
    at timestamptz NOT NULL,
    session_id text NOT NULL,
    run_id text NOT NULL DEFAULT '',
    kind text NOT NULL,
    value text NOT NULL DEFAULT '',
    source_name text NOT NULL DEFAULT '',
    call_key text NOT NULL DEFAULT '',
    score double precision NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX marks_once_idx ON marks (session_id, run_id, kind, value);
CREATE INDEX marks_at_idx ON marks (at);
