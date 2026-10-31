-- A computer is no longer pinned or archived: only PATCH /api/computers/{id}
-- set them, and the API serves no such route now (docs/DECISIONS.md, "The
-- coding layer is removed"). The columns had no index and no other reader, so
-- dropping them changes no row that remains.

ALTER TABLE computers DROP COLUMN pinned;
ALTER TABLE computers DROP COLUMN archived;
