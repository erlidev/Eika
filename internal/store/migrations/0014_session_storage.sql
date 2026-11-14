ALTER TABLE sessions DROP CONSTRAINT sessions_kind_check;
ALTER TABLE sessions
    ADD CONSTRAINT sessions_kind_check CHECK (kind IN ('user', 'home', 'subagent', 'routine')),
    ADD COLUMN owner text NOT NULL DEFAULT 'local' CHECK (btrim(owner) <> ''),
    ADD COLUMN agent text NOT NULL DEFAULT '',
    ADD COLUMN name text NOT NULL DEFAULT '',
    ADD COLUMN project_id text REFERENCES projects (id) ON DELETE RESTRICT,
    ADD COLUMN parent_session_id text REFERENCES sessions (id) ON DELETE RESTRICT,
    ADD COLUMN state text NOT NULL DEFAULT 'idle'
        CHECK (state IN ('idle', 'running', 'waiting', 'failed', 'cancelled')),
    ADD COLUMN brief text NOT NULL DEFAULT '',
    ADD COLUMN result text NOT NULL DEFAULT '',
    ADD COLUMN last_active_at timestamptz;
UPDATE sessions SET last_active_at = updated_at;
ALTER TABLE sessions
    ALTER COLUMN last_active_at SET NOT NULL,
    ALTER COLUMN last_active_at SET DEFAULT now(),
    ADD CONSTRAINT sessions_identity_check CHECK (
        kind = 'user' OR (btrim(agent) <> '' AND btrim(name) <> '')),
    ADD CONSTRAINT sessions_parent_check CHECK (
        (kind <> 'home' OR parent_session_id IS NULL) AND
        (kind <> 'subagent' OR parent_session_id IS NOT NULL) AND
        parent_session_id IS DISTINCT FROM id);
CREATE UNIQUE INDEX sessions_home_identity_idx ON sessions (owner, name) WHERE kind = 'home';
CREATE UNIQUE INDEX sessions_subagent_name_idx ON sessions (parent_session_id, name) WHERE kind = 'subagent';
CREATE INDEX sessions_project_idx ON sessions (project_id);
CREATE INDEX sessions_parent_idx ON sessions (parent_session_id);
ALTER TABLE session_entries DROP CONSTRAINT session_entries_kind_check;
ALTER TABLE session_entries
    ADD CONSTRAINT session_entries_kind_check CHECK (kind IN
        ('user', 'assistant', 'tool_call', 'tool_result', 'system', 'event', 'compaction', 'preamble', 'notice')),
    ADD COLUMN channel text NOT NULL DEFAULT '';
