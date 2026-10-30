-- The Eika schema, squashed from the coding harness's migrations when Eika
-- became a personal assistant (docs/DECISIONS.md, "Fresh baseline schema").
-- Everything lives in schema core: the store creates it before migrating and
-- names it as every connection's search_path. Sentry's tables get a schema of
-- their own.
--
-- Until phase 0 of docs/PLAN.md closes, a schema change edits this file
-- rather than adding a migration, since a fresh database is expected.

CREATE TABLE settings (
    key   text PRIMARY KEY,
    value jsonb NOT NULL
);

-- Projects are directories on the Docker host the coding harness mounted. A
-- remote project (cloned from a git remote) can no longer be created.
CREATE TABLE projects (
    id              text PRIMARY KEY,
    name            text NOT NULL UNIQUE,
    kind            text NOT NULL CHECK (kind IN ('remote', 'local')),
    remote_url      text NOT NULL DEFAULT '',
    host_path       text NOT NULL DEFAULT '',
    default_branch  text NOT NULL DEFAULT 'main',
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- sandbox is what a workspace's container may consume, reach, and expose: its
-- CPU, memory, and process limits, its egress mode and allowlist, and the
-- ports the harness forwards to it. It is the desired state, applied when the
-- workspace starts and whenever it changes; the empty object is no limits,
-- open egress, and no ports.
--
-- pinned and archived are the user's filing and change nothing about what a
-- workspace's container does.
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
    sandbox             jsonb NOT NULL DEFAULT '{}',
    pinned              boolean NOT NULL DEFAULT false,
    archived            boolean NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX workspaces_project_idx ON workspaces (project_id);
CREATE INDEX workspaces_parent_idx ON workspaces (parent_workspace_id);

-- Model providers and models. Every credential column holds what
-- internal/secret sealed; the harness never writes a credential here in the
-- clear.
CREATE TABLE providers (
    id         text PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    kind       text NOT NULL,
    base_url   text NOT NULL,
    api_key    bytea,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- name is what Eika and its users call a model and is unique across every
-- provider, so a run or a setting can name one alone; model is the identifier
-- the provider's endpoint knows it by.
--
-- reasoning_efforts are the values the model offers: compatible endpoints
-- disagree on the vocabulary, so the choices belong to the model rather than
-- to a list in the code, and reasoning_effort is the one in force.
-- thinking_switch is the request field that turns thinking off when the effort
-- is "none", since an endpoint may reject a whole request for a field it does
-- not know. image_input says the model reads images; until the user says so
-- a request carries none.
CREATE TABLE models (
    id                text PRIMARY KEY,
    provider_id       text NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
    name              text NOT NULL UNIQUE,
    model             text NOT NULL,
    context_window    integer NOT NULL CHECK (context_window > 0),
    max_output        integer NOT NULL CHECK (max_output > 0),
    reasoning_effort  text NOT NULL DEFAULT '',
    reasoning_efforts text[] NOT NULL DEFAULT '{}',
    thinking_switch   text NOT NULL DEFAULT 'reasoning_effort'
        CHECK (thinking_switch IN ('reasoning_effort', 'chat_template_kwargs', 'thinking')),
    preserve_thinking boolean NOT NULL DEFAULT false,
    image_input       boolean NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX models_provider_idx ON models (provider_id);

-- A profile is a named configuration of what a run sends. Every setting is
-- nullable and NULL is "not set here", which falls through to the next
-- layer: a session's overrides sit above the profile, the model row and the
-- provider's defaults below it.
--
-- model_id names the model by id, so that renaming the model does not break
-- the profile; NULL is the default model. workspace_prompt and chat_prompt
-- replace the built-in base prompts when set, the empty string included.
-- context_files NULL reads them. tools is in the shape of sessions.tools,
-- with mcp__<server>__* standing for every tool of that server; NULL is
-- every tool. sampling is a provider.Sampling: the keys it has are the
-- parameters set, so '{}' sets none. preserve_thinking NULL leaves the
-- model row's switch in force.
CREATE TABLE profiles (
    id                text PRIMARY KEY,
    name              text NOT NULL UNIQUE,
    description       text NOT NULL DEFAULT '',
    model_id          text REFERENCES models (id) ON DELETE SET NULL,
    workspace_prompt  text,
    chat_prompt       text,
    instructions      text,
    context_files     boolean,
    tools             text[],
    sampling          jsonb NOT NULL DEFAULT '{}',
    preserve_thinking boolean,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- A deployment that never opens the profile editor runs on one profile that
-- sets nothing. The id is generated here in the form store.NewID gives every
-- other row.
INSERT INTO profiles (id, name)
SELECT string_agg(substr('abcdefghijklmnopqrstuvwxyz234567', 1 + floor(random() * 32)::int, 1), ''), 'Default'
FROM generate_series(1, 20);

-- A session with no workspace is a chat. kind says who opened it: the user,
-- or a fork of another session.
--
-- head_entry_id has no foreign key: session_entries references sessions, so a
-- key in the other direction would be a cycle. SetSessionHead checks that the
-- entry belongs to the session instead.
--
-- tools NULL is every tool the session can run; an array is the ones the user
-- chose, possibly none. profile_id NULL is whichever profile is the default
-- when a run starts. overrides holds the profile settings the session sets
-- for itself, in the profile's shape without its name, description, and
-- tools; a key it lacks is inherited. An untitled session shows a placeholder
-- until its first run names it. pinned and archived are the user's filing.
CREATE TABLE sessions (
    id                text PRIMARY KEY,
    workspace_id      text REFERENCES workspaces (id) ON DELETE CASCADE,
    title             text NOT NULL DEFAULT '',
    untitled          boolean NOT NULL DEFAULT false,
    kind              text NOT NULL DEFAULT 'user' CHECK (kind IN ('user', 'fork')),
    head_entry_id     text,
    parent_session_id text REFERENCES sessions (id) ON DELETE SET NULL,
    tools             text[],
    profile_id        text REFERENCES profiles (id) ON DELETE SET NULL,
    overrides         jsonb,
    pinned            boolean NOT NULL DEFAULT false,
    archived          boolean NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_workspace_idx ON sessions (workspace_id);
CREATE INDEX sessions_parent_idx ON sessions (parent_session_id);
CREATE INDEX sessions_chat_idx ON sessions (created_at) WHERE workspace_id IS NULL;
CREATE INDEX sessions_profile_idx ON sessions (profile_id);

-- A compaction entry holds a summary that replaces the conversation before it,
-- except its newest messages, on every path through it.
CREATE TABLE session_entries (
    id         text PRIMARY KEY,
    session_id text NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    parent_id  text REFERENCES session_entries (id) ON DELETE CASCADE,
    seq        bigint NOT NULL,
    kind       text NOT NULL
        CHECK (kind IN ('user', 'assistant', 'tool_call', 'tool_result', 'system', 'event', 'compaction')),
    payload    jsonb NOT NULL,
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

-- One row per model call a run made. The messages are not copied: they are
-- the session's path down to entry_id, which the session already stores, so
-- a record costs the system prompt's sections and the tool schemas. model is
-- the model's name when the call was made, which outlives a deleted model.
--
-- entry_id has no foreign key, like sessions.head_entry_id: an entry goes
-- only with its session, which takes its records with it, and a key would
-- make every entry deleted with a session look this table up. The insert
-- checks that the entry belongs to the session instead.
CREATE TABLE model_requests (
    id             text PRIMARY KEY,
    session_id     text NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    run_id         text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    entry_id       text,
    model_id       text REFERENCES models (id) ON DELETE SET NULL,
    model          text NOT NULL,
    sections       jsonb NOT NULL DEFAULT '[]',
    tools          jsonb NOT NULL DEFAULT '[]',
    parameters     jsonb NOT NULL DEFAULT '{}',
    message_tokens integer NOT NULL DEFAULT 0 CHECK (message_tokens >= 0),
    input_tokens   integer NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens  integer NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    total_tokens   integer NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX model_requests_session_idx ON model_requests (session_id, created_at);
CREATE INDEX model_requests_run_idx ON model_requests (run_id);

-- The one sign-in password, as a PBKDF2 hash. The check keeps it one row.
CREATE TABLE auth_password (
    id         integer PRIMARY KEY CHECK (id = 1),
    hash       text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- A signed-in browser. The token is never stored, only its SHA-256.
CREATE TABLE auth_sessions (
    token_hash text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX auth_sessions_expiry_idx ON auth_sessions (expires_at);

-- The API keys of the keyed search providers and GitHub, and each quota
-- bucket's usage, so that a monthly quota survives a restart. day and month
-- are the UTC day ("2026-09-18") and month ("2026-09") the counters count in;
-- a row from an earlier day reads as zero for that day.
CREATE TABLE search_keys (
    name       text PRIMARY KEY,
    key        bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE search_usage (
    name           text PRIMARY KEY,
    day            text NOT NULL,
    day_used       integer NOT NULL CHECK (day_used >= 0),
    month          text NOT NULL,
    month_used     integer NOT NULL CHECK (month_used >= 0),
    cooldown_until timestamptz,
    fail_streak    integer NOT NULL DEFAULT 0 CHECK (fail_streak >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- MCP servers the user configured. kind is http for a remote server the
-- harness reaches, stdio for one it starts in a workspace. The headers and
-- the environment are sealed JSON, since both commonly carry tokens.
CREATE TABLE mcp_servers (
    id                  text PRIMARY KEY,
    name                text NOT NULL UNIQUE,
    kind                text NOT NULL CHECK (kind IN ('http', 'stdio')),
    url                 text NOT NULL DEFAULT '',
    headers             bytea,
    command             text NOT NULL DEFAULT '',
    args                text[] NOT NULL DEFAULT '{}',
    env                 bytea,
    enabled             boolean NOT NULL DEFAULT true,
    disabled_tools      text[] NOT NULL DEFAULT '{}',
    oauth_client_id     text NOT NULL DEFAULT '',
    oauth_client_secret bytea,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CHECK (kind <> 'http' OR url <> ''),
    CHECK (kind <> 'stdio' OR command <> '')
);

-- One row per MCP server: the authorization server it was authorized with,
-- its metadata as discovered, the client registered there, and the sealed
-- tokens. A row with no tokens keeps a registered client for the next
-- authorization.
CREATE TABLE mcp_credentials (
    server_id             text PRIMARY KEY REFERENCES mcp_servers (id) ON DELETE CASCADE,
    issuer                text NOT NULL DEFAULT '',
    resource              text NOT NULL DEFAULT '',
    resource_metadata_url text NOT NULL DEFAULT '',
    metadata              jsonb NOT NULL DEFAULT '{}',
    client_id             text NOT NULL DEFAULT '',
    client_secret         bytea,
    client_auth_method    text NOT NULL DEFAULT '',
    client_registration   text NOT NULL DEFAULT '',
    redirect_uri          text NOT NULL DEFAULT '',
    access_token          bytea,
    refresh_token         bytea,
    scope                 text NOT NULL DEFAULT '',
    expires_at            timestamptz,
    updated_at            timestamptz NOT NULL DEFAULT now()
);
