package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MCPServer is one MCP server the user configured.
type MCPServer struct {
	ID   string
	Name string
	// Kind is http for a remote server, stdio for one started in a
	// workspace.
	Kind string
	URL  string
	// Headers is the JSON of the headers every request carries, as the
	// harness sealed it; nil when there are none.
	Headers []byte
	Command string
	Args    []string
	// Env is the JSON of the process's environment, sealed like Headers.
	Env           []byte
	Enabled       bool
	DisabledTools []string
	// OAuthClientID is a client the user registered by hand with the
	// server's authorization server; OAuthClientSecret is its sealed
	// secret.
	OAuthClientID     string
	OAuthClientSecret []byte
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// mcpServerColumns is the column list every MCP server query selects, in
// the order scanMCPServer reads them.
const mcpServerColumns = `id, name, kind, url, headers, command, args, env, enabled, disabled_tools,
	oauth_client_id, oauth_client_secret, created_at, updated_at`

// CreateMCPServer inserts m and returns it as stored. An empty ID gets a
// fresh one; a duplicate name is ErrConflict.
func (s *Store) CreateMCPServer(ctx context.Context, m MCPServer) (MCPServer, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	const q = `INSERT INTO mcp_servers (id, name, kind, url, headers, command, args, env, enabled, disabled_tools,
			oauth_client_id, oauth_client_secret)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING ` + mcpServerColumns
	out, err := scanMCPServer(s.pool.QueryRow(ctx, q, m.ID, m.Name, m.Kind, m.URL, m.Headers, m.Command, textArray(m.Args),
		m.Env, m.Enabled, textArray(m.DisabledTools), m.OAuthClientID, m.OAuthClientSecret))
	if err != nil {
		return MCPServer{}, wrap("create mcp server "+m.Name, err)
	}
	return out, nil
}

// MCPServer returns the MCP server with the given id.
func (s *Store) MCPServer(ctx context.Context, id string) (MCPServer, error) {
	m, err := scanMCPServer(s.pool.QueryRow(ctx, `SELECT `+mcpServerColumns+` FROM mcp_servers WHERE id = $1`, id))
	if err != nil {
		return MCPServer{}, wrap("read mcp server "+id, err)
	}
	return m, nil
}

// MCPServers returns every MCP server, by name.
func (s *Store) MCPServers(ctx context.Context) ([]MCPServer, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mcpServerColumns+` FROM mcp_servers ORDER BY name`)
	if err != nil {
		return nil, wrap("list mcp servers", err)
	}
	defer rows.Close()
	var out []MCPServer
	for rows.Next() {
		m, err := scanMCPServer(rows)
		if err != nil {
			return nil, wrap("list mcp servers", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list mcp servers", err)
	}
	return out, nil
}

// UpdateMCPServer writes every field of m but its kind over the stored row
// and returns the row as it now stands. A name another server has is
// ErrConflict. A new name is carried into the tool choices of profiles and
// sessions, whose entries name the server's tools by it.
func (s *Store) UpdateMCPServer(ctx context.Context, m MCPServer) (MCPServer, error) {
	op := "update mcp server " + m.ID
	var out MCPServer
	err := s.tx(ctx, func(q querier) error {
		var old string
		if err := q.QueryRow(ctx, `SELECT name FROM mcp_servers WHERE id = $1 FOR UPDATE`, m.ID).Scan(&old); err != nil {
			return wrap(op, err)
		}
		const update = `UPDATE mcp_servers SET name = $2, url = $3, headers = $4, command = $5, args = $6, env = $7,
				enabled = $8, disabled_tools = $9, oauth_client_id = $10, oauth_client_secret = $11, updated_at = now()
			WHERE id = $1
			RETURNING ` + mcpServerColumns
		var err error
		out, err = scanMCPServer(q.QueryRow(ctx, update, m.ID, m.Name, m.URL, m.Headers, m.Command, textArray(m.Args), m.Env,
			m.Enabled, textArray(m.DisabledTools), m.OAuthClientID, m.OAuthClientSecret))
		if err != nil {
			return wrap(op, err)
		}
		if old != out.Name {
			return renameToolChoices(ctx, q, op, old, out.Name)
		}
		return nil
	})
	if err != nil {
		return MCPServer{}, err
	}
	return out, nil
}

// renameToolChoices rewrites the tool choice entries that name a server's
// tools, mcp__<old>__<tool> and mcp__<old>__*, to name them by the server's
// new name. A server name holds no double underscore, so the prefix ends
// where the server's name does.
func renameToolChoices(ctx context.Context, q querier, op, old, name string) error {
	oldPrefix, newPrefix := "mcp__"+old+"__", "mcp__"+name+"__"
	for _, table := range []string{"profiles", "sessions"} {
		stmt := `UPDATE ` + table + ` SET tools = ARRAY(
				SELECT CASE WHEN starts_with(t, $1) THEN $2 || substr(t, length($1) + 1) ELSE t END
				FROM unnest(tools) WITH ORDINALITY AS u(t, n) ORDER BY n)
			WHERE EXISTS (SELECT 1 FROM unnest(tools) AS t WHERE starts_with(t, $1))`
		if _, err := q.Exec(ctx, stmt, oldPrefix, newPrefix); err != nil {
			return wrap(op+": rename tool choices in "+table, err)
		}
	}
	return nil
}

// DeleteMCPServer removes an MCP server and its credentials.
func (s *Store) DeleteMCPServer(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM mcp_servers WHERE id = $1`, id)
	if err != nil {
		return wrap("delete mcp server "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("delete mcp server "+id, pgx.ErrNoRows)
	}
	return nil
}

func scanMCPServer(row pgx.Row) (MCPServer, error) {
	var m MCPServer
	if err := row.Scan(&m.ID, &m.Name, &m.Kind, &m.URL, &m.Headers, &m.Command, &m.Args, &m.Env, &m.Enabled,
		&m.DisabledTools, &m.OAuthClientID, &m.OAuthClientSecret, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return MCPServer{}, err
	}
	m.CreatedAt, m.UpdatedAt = m.CreatedAt.UTC(), m.UpdatedAt.UTC()
	return m, nil
}

// MCPCredentials is what the harness holds to authorize with one MCP
// server. The secrets are sealed; the store never sees them in the clear.
type MCPCredentials struct {
	ServerID            string
	Issuer              string
	Resource            string
	ResourceMetadataURL string
	// Metadata is the authorization server's metadata as JSON.
	Metadata           []byte
	ClientID           string
	ClientSecret       []byte
	ClientAuthMethod   string
	ClientRegistration string
	RedirectURI        string
	AccessToken        []byte
	RefreshToken       []byte
	Scope              string
	// ExpiresAt is zero when the access token has no known expiry.
	ExpiresAt time.Time
	UpdatedAt time.Time
}

// MCPCredentials returns a server's credentials, ErrNotFound when it has
// none.
func (s *Store) MCPCredentials(ctx context.Context, serverID string) (MCPCredentials, error) {
	const q = `SELECT server_id, issuer, resource, resource_metadata_url, metadata, client_id, client_secret,
			client_auth_method, client_registration, redirect_uri, access_token, refresh_token, scope,
			expires_at, updated_at
		FROM mcp_credentials WHERE server_id = $1`
	var (
		c       MCPCredentials
		expires *time.Time
	)
	err := s.pool.QueryRow(ctx, q, serverID).Scan(&c.ServerID, &c.Issuer, &c.Resource, &c.ResourceMetadataURL,
		&c.Metadata, &c.ClientID, &c.ClientSecret, &c.ClientAuthMethod, &c.ClientRegistration, &c.RedirectURI,
		&c.AccessToken, &c.RefreshToken, &c.Scope, &expires, &c.UpdatedAt)
	if err != nil {
		return MCPCredentials{}, wrap("read mcp credentials "+serverID, err)
	}
	c.ExpiresAt, c.UpdatedAt = stamp(expires), c.UpdatedAt.UTC()
	return c, nil
}

// SetMCPCredentials stores a server's credentials, replacing any it had. A
// server that does not exist is ErrNotFound.
func (s *Store) SetMCPCredentials(ctx context.Context, c MCPCredentials) error {
	var expires *time.Time
	if !c.ExpiresAt.IsZero() {
		expires = &c.ExpiresAt
	}
	metadata := c.Metadata
	if len(metadata) == 0 {
		metadata = []byte(`{}`)
	}
	const q = `INSERT INTO mcp_credentials (server_id, issuer, resource, resource_metadata_url, metadata, client_id,
			client_secret, client_auth_method, client_registration, redirect_uri, access_token, refresh_token,
			scope, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (server_id) DO UPDATE SET issuer = excluded.issuer, resource = excluded.resource,
			resource_metadata_url = excluded.resource_metadata_url, metadata = excluded.metadata,
			client_id = excluded.client_id, client_secret = excluded.client_secret,
			client_auth_method = excluded.client_auth_method, client_registration = excluded.client_registration,
			redirect_uri = excluded.redirect_uri, access_token = excluded.access_token,
			refresh_token = excluded.refresh_token, scope = excluded.scope, expires_at = excluded.expires_at,
			updated_at = now()`
	_, err := s.pool.Exec(ctx, q, c.ServerID, c.Issuer, c.Resource, c.ResourceMetadataURL, metadata, c.ClientID,
		c.ClientSecret, c.ClientAuthMethod, c.ClientRegistration, c.RedirectURI, c.AccessToken, c.RefreshToken,
		c.Scope, expires)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return wrap("write mcp credentials "+c.ServerID, pgx.ErrNoRows)
		}
		return wrap("write mcp credentials "+c.ServerID, err)
	}
	return nil
}
