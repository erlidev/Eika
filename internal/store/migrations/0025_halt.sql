-- Whether Eika is halted, and since when. One row, so the state survives a
-- restart and a halt is never half recorded: the row changes in the same
-- transaction as the audit row that says why.
CREATE TABLE halt (
    id     boolean PRIMARY KEY DEFAULT true CHECK (id),
    halted boolean NOT NULL DEFAULT false,
    at     timestamptz,
    reason text NOT NULL DEFAULT ''
);
INSERT INTO halt (id) VALUES (true);
