//go:build docker

package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/jackc/pgx/v5"
)

// A provider that exists before the protocol was a column keeps speaking Chat
// Completions, with nothing marked as the user's own, until a Responses check
// finds otherwise.
func TestUpgradeFrom0006LeavesProvidersOnChat(t *testing.T) {
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	names := []string{
		"0001_baseline", "0002_remove_session_forks", "0003_remove_computer_filing",
		"0004_provider_probe", "0005_model_request_usage", "0006_model_calls",
	}
	statements := []string{`CREATE SCHEMA core`, `SET search_path = core`}
	for _, name := range names {
		sql, err := os.ReadFile("migrations/" + name + ".sql")
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		statements = append(statements, string(sql))
	}
	statements = append(statements,
		`CREATE TABLE schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
		`INSERT INTO schema_migrations (version) VALUES ('0001_baseline'), ('0002_remove_session_forks'), ('0003_remove_computer_filing'), ('0004_provider_probe'), ('0005_model_request_usage'), ('0006_model_calls')`,
		`INSERT INTO providers (id, name, kind, base_url, backend, overridden) VALUES ('p', 'bench', 'openai', 'http://m.invalid/v1', 'llamacpp', '{kv}')`,
	)
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("set up the 0006 database: %v\n%s", err, stmt)
		}
	}

	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	p, err := st.Provider(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if p.API != "chat" || p.Backend != "llamacpp" || len(p.Overridden) != 1 || p.Overridden[0] != "kv" {
		t.Errorf("provider after the upgrade = %+v, want the chat api and everything else as it was", p)
	}
}
