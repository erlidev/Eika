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

// Entries written while the payload was jsonb are still there, with the
// document they held, after the column becomes json; new ones keep their text.
func TestUpgradeFrom0009KeepsTheEntriesItHolds(t *testing.T) {
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	names := []string{
		"0001_baseline", "0002_remove_session_forks", "0003_remove_computer_filing", "0004_provider_probe",
		"0005_model_request_usage", "0006_model_calls", "0007_provider_api", "0008_model_classes", "0009_call_phases",
	}
	statements := []string{`CREATE SCHEMA core`, `SET search_path = core`}
	applied := ""
	for i, name := range names {
		sql, err := os.ReadFile("migrations/" + name + ".sql")
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		statements = append(statements, string(sql))
		if i > 0 {
			applied += ", "
		}
		applied += "('" + name + "')"
	}
	statements = append(statements,
		`CREATE TABLE schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
		`INSERT INTO schema_migrations (version) VALUES `+applied,
		`INSERT INTO computers (id, name, state) VALUES ('c', 'old', 'running')`,
		`INSERT INTO sessions (id, computer_id, title) VALUES ('s', 'c', 'old')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES
		   ('e1', 's', 1, 'user', '{"role": "user", "content": "hi"}')`,
	)
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("set up the 0009 database: %v\n%s", err, stmt)
		}
	}
	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)

	e, err := st.Entry(ctx, "e1")
	if err != nil {
		t.Fatalf("read the entry written before the upgrade: %v", err)
	}
	if got, want := string(e.Payload), `{"role": "user", "content": "hi"}`; got != want {
		t.Errorf("old payload = %s, want %s", got, want)
	}
	added, err := st.AppendEntry(ctx, "s", store.Entry{Kind: store.KindUser, Payload: []byte(`{"content":"b","role":"user"}`)})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	if again, err := st.Entry(ctx, added.ID); err != nil || string(again.Payload) != `{"content":"b","role":"user"}` {
		t.Errorf("new payload = %s, %v; want the text it was written as", again.Payload, err)
	}
}
