-- Model providers, models, sign-in, and git remote credentials move from the
-- deployment's configuration and environment into the database, where the web
-- UI edits them. Every credential column holds what internal/secret sealed;
-- the harness never writes a credential here in the clear.

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
-- provider, so a run, a setting, or a spawn_agent call can name one alone;
-- model is the identifier the provider's endpoint knows it by.
CREATE TABLE models (
    id                text PRIMARY KEY,
    provider_id       text NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
    name              text NOT NULL UNIQUE,
    model             text NOT NULL,
    context_window    integer NOT NULL CHECK (context_window > 0),
    max_output        integer NOT NULL CHECK (max_output > 0),
    reasoning_effort  text NOT NULL DEFAULT '',
    preserve_thinking boolean NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX models_provider_idx ON models (provider_id);

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

-- A remote's credentials were the names of harness environment variables.
-- They are now entered with the project and sealed, so the references go;
-- a private remote's credentials are entered again in the project's settings.
ALTER TABLE projects
    DROP COLUMN remote_username_env,
    DROP COLUMN remote_password_env,
    ADD COLUMN remote_username text NOT NULL DEFAULT '',
    ADD COLUMN remote_password bytea;
