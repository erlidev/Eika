-- The initial Eika schema: projects and the workspaces, sessions, runs and
-- subagents that hang off them, plus the key/value settings table.

CREATE TABLE projects (
    id             text PRIMARY KEY,
    name           text NOT NULL UNIQUE,
    kind           text NOT NULL CHECK (kind IN ('remote', 'local')),
    remote_url     text NOT NULL DEFAULT '',
    host_path      text NOT NULL DEFAULT '',
    default_branch text NOT NULL DEFAULT 'main',
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspaces (
    id                  text PRIMARY KEY,
    project_id          text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name                text NOT NULL,
    branch              text NOT NULL,
    base_commit         text NOT NULL DEFAULT '',
    image               text NOT NULL DEFAULT '',
    state               text NOT NULL,
    container_id        text NOT NULL DEFAULT '',
    parent_workspace_id text REFERENCES workspaces (id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX workspaces_project_idx ON workspaces (project_id);
CREATE INDEX workspaces_parent_idx ON workspaces (parent_workspace_id);

-- head_entry_id has no foreign key: session_entries references sessions, so a
-- key in the other direction would be a cycle. SetSessionHead checks that the
-- entry belongs to the session instead.
CREATE TABLE sessions (
    id                text PRIMARY KEY,
    workspace_id      text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    title             text NOT NULL DEFAULT '',
    head_entry_id     text,
    parent_session_id text REFERENCES sessions (id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_workspace_idx ON sessions (workspace_id);
CREATE INDEX sessions_parent_idx ON sessions (parent_session_id);

CREATE TABLE session_entries (
    id         text PRIMARY KEY,
    session_id text NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    parent_id  text REFERENCES session_entries (id) ON DELETE CASCADE,
    seq        bigint NOT NULL,
    kind       text NOT NULL CHECK (kind IN ('user', 'assistant', 'tool_call', 'tool_result', 'system', 'event')),
    payload    jsonb NOT NULL,
    commit_sha text,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- The unique constraint is also the (session_id, seq) index a path walk
    -- and an outline order by.
    UNIQUE (session_id, seq)
);

CREATE INDEX session_entries_parent_idx ON session_entries (session_id, parent_id);

CREATE TABLE runs (
    id          text PRIMARY KEY,
    session_id  text NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    state       text NOT NULL CHECK (state IN ('running', 'done', 'error', 'aborted')),
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    error       text NOT NULL DEFAULT ''
);

CREATE INDEX runs_session_idx ON runs (session_id, started_at);

CREATE TABLE subagents (
    id                 text PRIMARY KEY,
    parent_session_id  text NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    child_session_id   text NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    child_workspace_id text NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    state              text NOT NULL CHECK (state IN ('running', 'done', 'error', 'aborted')),
    result             text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    finished_at        timestamptz
);

CREATE INDEX subagents_parent_idx ON subagents (parent_session_id);
CREATE INDEX subagents_child_session_idx ON subagents (child_session_id);
CREATE INDEX subagents_child_workspace_idx ON subagents (child_workspace_id);

CREATE TABLE settings (
    key   text PRIMARY KEY,
    value jsonb NOT NULL
);
