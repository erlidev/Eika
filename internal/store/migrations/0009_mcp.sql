-- MCP servers the user configured, and the OAuth credentials the harness
-- holds for them. Everything secret is sealed by internal/secret: the
-- headers and the environment are sealed JSON, since both commonly carry
-- tokens, and so are the client secret and the tokens.

-- kind is http for a remote server the harness reaches, stdio for one it
-- starts in a workspace. An http server has a url; a stdio server has a
-- command, its arguments, and its environment.
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

-- One row per server: the authorization server it was authorized with, its
-- metadata as discovered, the client registered there, and the tokens. A
-- row with no tokens keeps a registered client for the next authorization.
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
