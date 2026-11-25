-- Older rows have no stored source; reinstall supplies it.
ALTER TABLE skills ADD COLUMN source text NOT NULL DEFAULT '';
