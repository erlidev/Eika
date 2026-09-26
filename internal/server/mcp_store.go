package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/mcp/oauth"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/store"
)

// sealPairs seals headers or an environment as the JSON of their map.
func (s *Server) sealPairs(pairs map[string]string) ([]byte, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(pairs)
	if err != nil {
		return nil, fmt.Errorf("encode mcp server secrets: %w", err)
	}
	return s.deps.Secrets.Seal(string(data))
}

// mergePairs applies a PATCH's headers or environment to the sealed ones
// stored: absent keeps them (or, with drop, removes them), and a null value
// keeps the one stored under its name.
func (s *Server) mergePairs(sealed []byte, update map[string]*string, drop bool) (map[string]string, error) {
	if update == nil && drop {
		return map[string]string{}, nil
	}
	var stored map[string]string
	needStored := update == nil
	for _, v := range update {
		needStored = needStored || v == nil
	}
	if needStored {
		var err error
		if stored, err = openPairs(s.deps.Secrets, sealed); err != nil {
			return nil, conflictf("the stored values cannot be read, which happens when the harness's secret key file changes; enter every value again")
		}
	}
	if update == nil {
		return stored, nil
	}
	out := make(map[string]string, len(update))
	for name, v := range update {
		if v != nil {
			out[name] = *v
			continue
		}
		old, ok := stored[name]
		if !ok {
			return nil, invalidf("%s has no stored value to keep; give it one", name)
		}
		out[name] = old
	}
	return out, nil
}

// openPairs opens headers or an environment sealPairs sealed.
func openPairs(box *secret.Box, sealed []byte) (map[string]string, error) {
	plain, err := box.Open(sealed)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if plain == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(plain), &out); err != nil {
		return nil, fmt.Errorf("decode mcp server secrets: %w", err)
	}
	return out, nil
}

// forgetMCPTokens drops a server's tokens and keeps its registered client.
func (s *Server) forgetMCPTokens(ctx context.Context, id string) error {
	backend := s.mcpBackend()
	creds, ok, err := backend.MCPCredentials(ctx, id)
	if err != nil || !ok {
		return err
	}
	creds.AccessToken, creds.RefreshToken, creds.ExpiresAt, creds.Scope = "", "", time.Time{}, ""
	return backend.SaveMCPCredentials(ctx, id, creds)
}

// unreadableMCP words a server secret the harness cannot open.
func (s *Server) unreadableMCP(row store.MCPServer, err error) error {
	s.log.Error("open mcp server secret", "server_id", row.ID, "error", err)
	return conflictf("the secrets of MCP server %s cannot be read, which happens when the harness's secret key file changes; enter them again", row.Name)
}

// storeFailure marks a failure of the database behind the MCP pool, which
// stays internal however the pool reports it.
type storeFailure struct{ err error }

// Error returns the store's message.
func (f storeFailure) Error() string { return f.err.Error() }

// Unwrap returns the store's error, so its sentinels still map.
func (f storeFailure) Unwrap() error { return f.err }

// mcpStore is the store the MCP pool reads its servers and keeps their
// credentials through: the database, with every secret sealed.
type mcpStore struct {
	store   *store.Store
	secrets *secret.Box
	log     *slog.Logger
}

// mcpBackend returns the server's view of the pool's store.
func (s *Server) mcpBackend() mcpStore {
	return mcpStore{store: s.deps.Store, secrets: s.deps.Secrets, log: s.log}
}

// MCPServers returns every configured server. One whose secrets cannot be
// opened is left out, so the others' tools still reach runs.
func (b mcpStore) MCPServers(ctx context.Context) ([]mcp.ServerConfig, error) {
	rows, err := b.store.MCPServers(ctx)
	if err != nil {
		return nil, storeFailure{err}
	}
	out := make([]mcp.ServerConfig, 0, len(rows))
	for _, row := range rows {
		cfg, err := b.config(row)
		if err != nil {
			b.log.Error("open mcp server secrets", "server_id", row.ID, "error", err)
			continue
		}
		out = append(out, cfg)
	}
	return out, nil
}

// MCPServer returns one configured server with its secrets open.
func (b mcpStore) MCPServer(ctx context.Context, id string) (mcp.ServerConfig, error) {
	row, err := b.store.MCPServer(ctx, id)
	if err != nil {
		return mcp.ServerConfig{}, storeFailure{err}
	}
	cfg, err := b.config(row)
	if err != nil {
		b.log.Error("open mcp server secrets", "server_id", row.ID, "error", err)
		return mcp.ServerConfig{}, conflictf("the secrets of MCP server %s cannot be read, which happens when the harness's secret key file changes; enter them again", row.Name)
	}
	return cfg, nil
}

// config opens a server row's secrets.
func (b mcpStore) config(row store.MCPServer) (mcp.ServerConfig, error) {
	headers, err := openPairs(b.secrets, row.Headers)
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	env, err := openPairs(b.secrets, row.Env)
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	clientSecret, err := b.secrets.Open(row.OAuthClientSecret)
	if err != nil {
		return mcp.ServerConfig{}, err
	}
	cfg := mcp.ServerConfig{
		ID:                row.ID,
		Name:              row.Name,
		Kind:              row.Kind,
		URL:               row.URL,
		Headers:           headers,
		Command:           row.Command,
		Args:              row.Args,
		Enabled:           row.Enabled,
		DisabledTools:     row.DisabledTools,
		OAuthClientID:     row.OAuthClientID,
		OAuthClientSecret: clientSecret,
	}
	for _, name := range slices.Sorted(maps.Keys(env)) {
		cfg.Env = append(cfg.Env, name+"="+env[name])
	}
	return cfg, nil
}

// MCPCredentials returns a server's credentials with their secrets open.
// Credentials the harness can no longer open count as none, so the user
// authorizes again rather than meeting an error on every request.
func (b mcpStore) MCPCredentials(ctx context.Context, serverID string) (mcp.Credentials, bool, error) {
	row, err := b.store.MCPCredentials(ctx, serverID)
	if errors.Is(err, store.ErrNotFound) {
		return mcp.Credentials{}, false, nil
	}
	if err != nil {
		return mcp.Credentials{}, false, storeFailure{err}
	}
	var sealed [3]string
	for i, v := range [][]byte{row.ClientSecret, row.AccessToken, row.RefreshToken} {
		if sealed[i], err = b.secrets.Open(v); err != nil {
			b.log.Error("open mcp credentials", "server_id", serverID, "error", err)
			return mcp.Credentials{}, false, nil
		}
	}
	creds := mcp.Credentials{
		Issuer:              row.Issuer,
		Resource:            row.Resource,
		ResourceMetadataURL: row.ResourceMetadataURL,
		Client: oauth.Client{
			ID:           row.ClientID,
			Secret:       sealed[0],
			AuthMethod:   row.ClientAuthMethod,
			Registration: row.ClientRegistration,
		},
		RedirectURI:  row.RedirectURI,
		AccessToken:  sealed[1],
		RefreshToken: sealed[2],
		Scope:        row.Scope,
		ExpiresAt:    row.ExpiresAt,
		UpdatedAt:    row.UpdatedAt,
	}
	if err := json.Unmarshal(row.Metadata, &creds.Server); err != nil {
		return mcp.Credentials{}, false, fmt.Errorf("decode authorization server metadata of %s: %w", serverID, err)
	}
	return creds, true, nil
}

// SaveMCPCredentials seals and stores a server's credentials.
func (b mcpStore) SaveMCPCredentials(ctx context.Context, serverID string, c mcp.Credentials) error {
	metadata, err := json.Marshal(c.Server)
	if err != nil {
		return fmt.Errorf("encode authorization server metadata of %s: %w", serverID, err)
	}
	row := store.MCPCredentials{
		ServerID:            serverID,
		Issuer:              c.Issuer,
		Resource:            c.Resource,
		ResourceMetadataURL: c.ResourceMetadataURL,
		Metadata:            metadata,
		ClientID:            c.Client.ID,
		ClientAuthMethod:    c.Client.AuthMethod,
		ClientRegistration:  c.Client.Registration,
		RedirectURI:         c.RedirectURI,
		Scope:               c.Scope,
		ExpiresAt:           c.ExpiresAt,
	}
	for dst, v := range map[*[]byte]string{&row.ClientSecret: c.Client.Secret, &row.AccessToken: c.AccessToken, &row.RefreshToken: c.RefreshToken} {
		if *dst, err = b.secrets.Seal(v); err != nil {
			return err
		}
	}
	if err := b.store.SetMCPCredentials(ctx, row); err != nil {
		return storeFailure{err}
	}
	return nil
}
