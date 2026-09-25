-- Agent profiles, the overrides a session sets on its profile, and a record
-- of every model call.

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
-- parameters set, so '{}' sets none.
CREATE TABLE profiles (
    id               text PRIMARY KEY,
    name             text NOT NULL UNIQUE,
    description      text NOT NULL DEFAULT '',
    model_id         text REFERENCES models (id) ON DELETE SET NULL,
    workspace_prompt text,
    chat_prompt      text,
    instructions     text,
    context_files    boolean,
    tools            text[],
    sampling         jsonb NOT NULL DEFAULT '{}',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- A deployment that never opens the profile editor runs as it did before
-- profiles: one profile that sets nothing. The id is generated here in the
-- form store.NewID gives every other row.
INSERT INTO profiles (id, name)
SELECT string_agg(substr('abcdefghijklmnopqrstuvwxyz234567', 1 + floor(random() * 32)::int, 1), ''), 'Default'
FROM generate_series(1, 20);

-- profile_id NULL is whichever profile is the default when a run starts.
-- overrides holds the profile settings the session sets for itself, in the
-- profile's own shape without its name, description, and tools, since
-- sessions.tools already is the session's tool choice. A key it lacks is
-- inherited; NULL overrides nothing.
ALTER TABLE sessions
    ADD COLUMN profile_id text REFERENCES profiles (id) ON DELETE SET NULL,
    ADD COLUMN overrides  jsonb;

CREATE INDEX sessions_profile_idx ON sessions (profile_id);

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
