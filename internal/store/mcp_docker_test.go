//go:build docker

package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestMCPServerConstraints(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	if _, err := st.CreateMCPServer(ctx, store.MCPServer{Name: "docs", Kind: "http", URL: "https://mcp.example/mcp", Enabled: true}); err != nil {
		t.Fatalf("create remote: %v", err)
	}
	local, err := st.CreateMCPServer(ctx, store.MCPServer{Name: "files", Kind: "stdio", Command: "npx", Args: []string{"-y", "server"}})
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
	local.Name = "docs"
	if _, err := st.UpdateMCPServer(ctx, local); !errors.Is(err, store.ErrConflict) {
		t.Errorf("rename onto another = %v, want ErrConflict", err)
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
