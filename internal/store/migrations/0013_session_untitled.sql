-- An untitled session was created without a title and shows a placeholder
-- until its first run names it from the first message. A session from before
-- the column has the title it was given.
ALTER TABLE sessions ADD COLUMN untitled boolean NOT NULL DEFAULT false;
