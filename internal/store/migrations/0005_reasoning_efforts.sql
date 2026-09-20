-- The reasoning_effort values a model offers. Compatible endpoints disagree
-- on the vocabulary, so the choices belong to the model the user configured
-- rather than to a list in the code; reasoning_effort stays the one in force
-- and the UI cycles through this column.
ALTER TABLE models
    ADD COLUMN reasoning_efforts text[] NOT NULL DEFAULT '{}';
