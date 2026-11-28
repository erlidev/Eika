-- The Inbox entries tripwires make (docs/design/sentry.md, section 11): one row
-- for each tripwire that fired, open until the user answers "this was fine" or
-- "stop the run". The hold itself is in memory and ends with the run; the entry
-- stays. No foreign keys, so an entry outlives its session.
CREATE TABLE tripwire_entries (
    id          text PRIMARY KEY,
    at          timestamptz NOT NULL,
    tripwire    text NOT NULL,
    session_id  text NOT NULL,
    run_id      text NOT NULL,
    computer_id text NOT NULL DEFAULT '',
    host        text NOT NULL DEFAULT '',
    source      text NOT NULL DEFAULT '',
    held        boolean NOT NULL,
    summary     text NOT NULL,
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'fine', 'stopped')),
    answered_at timestamptz
);
CREATE INDEX tripwire_entries_open_idx ON tripwire_entries (at DESC) WHERE status = 'open';
