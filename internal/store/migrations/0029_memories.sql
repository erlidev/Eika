-- Index of memory files. The files in the memory repository are the source of
-- truth; a row mirrors one file's frontmatter, its body for search, and the
-- SHA-256 of the file's bytes so a changed file can be found and re-indexed.
-- Rows hold no foreign keys: a memory outlives any session named in source.
CREATE TABLE memories (
    name        text PRIMARY KEY,
    description text NOT NULL,
    type        text NOT NULL CHECK (type IN ('user', 'preference', 'person', 'fact', 'procedure')),
    source      text NOT NULL,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    expires     date,
    pinned      boolean NOT NULL DEFAULT false,
    state       text NOT NULL CHECK (state IN ('active', 'pending', 'archived')),
    content_sha text NOT NULL,
    body        text NOT NULL,
    body_tsv    tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('english', replace(name, '-', ' ')), 'A') ||
        setweight(to_tsvector('english', description), 'B') ||
        setweight(to_tsvector('english', left(body, 4096)), 'C')) STORED
);
CREATE INDEX memories_tsv_idx ON memories USING gin (body_tsv);
CREATE INDEX memories_state_idx ON memories (state, type, name);
