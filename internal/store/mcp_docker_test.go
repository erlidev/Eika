//go:build docker

package store_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestMCPServers(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	remote, err := st.CreateMCPServer(ctx, store.MCPServer{Name: "docs", Kind: "http", URL: "https://mcp.example/mcp", Headers: []byte("sealed"), Enabled: true})
	if err != nil {
		t.Fatalf("create remote: %v", err)
	}
	if remote.ID == "" || remote.Args == nil || remote.DisabledTools == nil || remote.CreatedAt.IsZero() {
		t.Errorf("created = %+v", remote)
	}
	local, err := st.CreateMCPServer(ctx, store.MCPServer{Name: "files", Kind: "stdio", Command: "npx", Args: []string{"-y", "server"}, Env: []byte("env")})
	if err != nil {
		t.Fatalf("create stdio: %v", err)
	}
	if _, err := st.CreateMCPServer(ctx, store.MCPServer{Name: "docs", Kind: "http", URL: "https://other.example"}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate name = %v, want ErrConflict", err)
	}
	if _, err := st.CreateMCPServer(ctx, store.MCPServer{Name: "nourl", Kind: "http"}); err == nil {
		t.Error("an http server with no url was stored")
	}
	if _, err := st.CreateMCPServer(ctx, store.MCPServer{Name: "bad", Kind: "websocket", URL: "wss://x"}); err == nil {
		t.Error("an unknown kind was stored")
	}

	list, err := st.MCPServers(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "docs" || list[1].Name != "files" {
		t.Fatalf("MCPServers = %+v, %v", list, err)
	}
	if !slices.Equal(list[1].Args, []string{"-y", "server"}) || string(list[1].Env) != "env" {
		t.Errorf("stdio server = %+v", list[1])
	}

	local.Name, local.Enabled, local.DisabledTools, local.OAuthClientID = "fs", false, []string{"write"}, "client"
	updated, err := st.UpdateMCPServer(ctx, local)
	if err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	if updated.Name != "fs" || updated.Enabled || !slices.Equal(updated.DisabledTools, []string{"write"}) || updated.OAuthClientID != "client" {
		t.Errorf("updated = %+v", updated)
	}
	local.Name = "docs"
	if _, err := st.UpdateMCPServer(ctx, local); !errors.Is(err, store.ErrConflict) {
		t.Errorf("rename onto another = %v, want ErrConflict", err)
	}

	if _, err := st.UpdateMCPServer(ctx, store.MCPServer{ID: "absent", Name: "absent"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update absent = %v, want ErrNotFound", err)
	}

	if err := st.DeleteMCPServer(ctx, local.ID); err != nil {
		t.Fatalf("DeleteMCPServer: %v", err)
	}
	if _, err := st.MCPServer(ctx, local.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("read deleted = %v, want ErrNotFound", err)
	}
	if err := st.DeleteMCPServer(ctx, local.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete twice = %v, want ErrNotFound", err)
	}
}

func TestMCPCredentials(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	srv, err := st.CreateMCPServer(ctx, store.MCPServer{Name: "docs", Kind: "http", URL: "https://mcp.example/mcp", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.MCPCredentials(ctx, srv.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("credentials before any = %v, want ErrNotFound", err)
	}
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	creds := store.MCPCredentials{
		ServerID: srv.ID, Issuer: "https://auth.example", Resource: "https://mcp.example/mcp",
		Metadata: []byte(`{"issuer": "https://auth.example"}`), ClientID: "c1", ClientRegistration: "dynamic",
		AccessToken: []byte("sealed-access"), RefreshToken: []byte("sealed-refresh"), Scope: "read", ExpiresAt: expires,
	}
	if err := st.SetMCPCredentials(ctx, creds); err != nil {
		t.Fatalf("SetMCPCredentials: %v", err)
	}
	got, err := st.MCPCredentials(ctx, srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientID != "c1" || string(got.AccessToken) != "sealed-access" || !got.ExpiresAt.Equal(expires) || got.Scope != "read" {
		t.Errorf("credentials = %+v", got)
	}

	// Signing out keeps the client and clears the tokens.
	creds.AccessToken, creds.RefreshToken, creds.ExpiresAt = nil, nil, time.Time{}
	if err := st.SetMCPCredentials(ctx, creds); err != nil {
		t.Fatal(err)
	}
	got, _ = st.MCPCredentials(ctx, srv.ID)
	if got.AccessToken != nil || !got.ExpiresAt.IsZero() || got.ClientID != "c1" {
		t.Errorf("credentials after clearing = %+v", got)
	}

	if err := st.SetMCPCredentials(ctx, store.MCPCredentials{ServerID: "missing"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("credentials for a missing server = %v, want ErrNotFound", err)
	}
	if err := st.DeleteMCPServer(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MCPCredentials(ctx, srv.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("credentials of a deleted server = %v, want them gone", err)
	}
}

func TestRenamingAnMCPServerCarriesItsNameIntoToolChoices(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	server, err := st.CreateMCPServer(ctx, store.MCPServer{Name: "docs", Kind: "http", URL: "https://mcp.example/mcp"})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	choice := []string{"bash", "mcp__docs__*", "mcp__docs__search", "mcp__docs2__*"}
	profile, err := st.CreateProfile(ctx, store.Profile{Name: "Docs", Tools: choice})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	sess := newSession(t, st, store.Session{Title: "s", Tools: choice})
	chat := newSession(t, st, store.Session{Title: "all tools"})

	server.Name = "manual"
	if _, err := st.UpdateMCPServer(ctx, server); err != nil {
		t.Fatalf("rename: %v", err)
	}

	want := []string{"bash", "mcp__manual__*", "mcp__manual__search", "mcp__docs2__*"}
	if p, err := st.Profile(ctx, profile.ID); err != nil || !slices.Equal(p.Tools, want) {
		t.Errorf("profile tools = %v, %v; want %v", p.Tools, err, want)
	}
	if s, err := st.Session(ctx, sess.ID); err != nil || !slices.Equal(s.Tools, want) {
		t.Errorf("session tools = %v, %v; want %v", s.Tools, err, want)
	}
	if s, err := st.Session(ctx, chat.ID); err != nil || s.Tools != nil {
		t.Errorf("a session choosing every tool = %v, %v; want nil", s.Tools, err)
	}
}
