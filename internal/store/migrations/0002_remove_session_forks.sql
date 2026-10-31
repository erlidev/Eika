-- Sessions are no longer forked (docs/DECISIONS.md, "The coding layer is
-- removed"). A fork was a session of its own with copied entries, so the ones
-- a database holds become ordinary sessions and lose only the pointer to where
-- they came from.
--
-- Dropping parent_session_id also drops sessions_parent_idx and the foreign
-- key. Nothing else wrote the column, and the design that gives it a new
-- meaning (docs/design/runtime.md, "Sessions") adds it back with that meaning.

UPDATE sessions SET kind = 'user' WHERE kind = 'fork';

ALTER TABLE sessions DROP CONSTRAINT sessions_kind_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_kind_check CHECK (kind IN ('user'));

ALTER TABLE sessions DROP COLUMN parent_session_id;
