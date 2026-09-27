-- Whether a model reads images. An endpoint may refuse a request that carries
-- images to a model that cannot read them, so a model sends none until the
-- user says it can; its conversation says in words that they were left out.
ALTER TABLE models ADD COLUMN image_input boolean NOT NULL DEFAULT false;
