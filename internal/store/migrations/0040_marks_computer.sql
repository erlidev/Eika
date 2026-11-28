-- A mark made by a tool call records the computer it ran on, so a chain can
-- join marks on one computer in one run (docs/design/sentry.md, section 11).
ALTER TABLE marks ADD COLUMN computer_id text NOT NULL DEFAULT '';
DROP INDEX marks_once_idx;
CREATE UNIQUE INDEX marks_once_idx ON marks (session_id, run_id, computer_id, kind, value);
