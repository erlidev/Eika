-- The private memory computer's id, recorded once per install. It names the
-- computer's container and volume (eika-cx-<id>), so two stacks that share a
-- Docker host never share a memory repository. The table has at most one row
-- and is not a computers row: the computers API, the UI and the tools never
-- see it.
-- Every install gets a generated id when the memory computer is first ensured.
CREATE TABLE memory_computer (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    id        text NOT NULL CHECK (id <> '')
);
