-- One row per change to what a home session's memory snapshot would hold:
-- a memory saved, edited, archived or purged. seq grows in commit order (the
-- writer takes an advisory lock), so a snapshot that records the highest seq
-- it read knows exactly which later changes the next message must name. Rows
-- hold names and the new state, never bodies.
CREATE TABLE memory_changes (
    seq    bigserial PRIMARY KEY,
    name   text NOT NULL,
    action text NOT NULL CHECK (action IN ('saved', 'edited', 'archived', 'purged')),
    state  text NOT NULL DEFAULT '',
    at     timestamptz NOT NULL DEFAULT now()
);
