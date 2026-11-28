-- The checkpoints eikad takes of a project tree when a run starts and when it
-- ends (docs/design/sentry.md, section 7). The commits are in the computer's
-- shadow repository; this row names the commit a run's card restores to and
-- records how long taking it took. No foreign keys, so a row outlives its
-- session and the 30-day sweep is the only thing that removes it.
CREATE TABLE checkpoints (
    id          text PRIMARY KEY,
    at          timestamptz NOT NULL,
    session_id  text NOT NULL,
    run_id      text NOT NULL,
    computer_id text NOT NULL,
    dir         text NOT NULL,
    label       text NOT NULL CHECK (label IN ('start', 'end')),
    commit_id   text NOT NULL,
    files       integer NOT NULL,
    skipped     integer NOT NULL,
    took_ms     bigint NOT NULL
);
CREATE INDEX checkpoints_run_idx ON checkpoints (run_id, at);
