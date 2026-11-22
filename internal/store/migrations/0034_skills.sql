-- Installed skills. The skills volume's directory is the source of truth for
-- the files; a row holds Eika's own settings (enabled, installed_at), the
-- hash of the installed tree, and what was parsed from SKILL.md at install so
-- the catalog and the install screen never read the volume. A row is created
-- disabled. broken is empty for a sound skill and otherwise says why the
-- directory no longer matches: a broken row is never loaded, whatever enabled
-- says. A row holds no foreign keys.
CREATE TABLE skills (
    name          text PRIMARY KEY CHECK (name ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(name) <= 64),
    enabled       boolean NOT NULL DEFAULT false,
    sha           text NOT NULL,
    installed_at  timestamptz NOT NULL,
    description   text NOT NULL,
    license       text NOT NULL DEFAULT '',
    compatibility text NOT NULL DEFAULT '',
    metadata      jsonb NOT NULL DEFAULT '{}',
    allowed_tools text NOT NULL DEFAULT '',
    body          text NOT NULL,
    files         jsonb NOT NULL DEFAULT '[]',
    warnings      jsonb NOT NULL DEFAULT '[]',
    screen        jsonb,
    broken        text NOT NULL DEFAULT ''
);
