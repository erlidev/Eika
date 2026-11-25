-- What Sentry's write check found in a pending memory (spans, highest score,
-- whether the guard answered), for the Inbox; and why a memory was archived.
-- Both live in the index only: the file holds neither.
ALTER TABLE memories
    ADD COLUMN screen jsonb,
    ADD COLUMN archive_reason text NOT NULL DEFAULT '';
