//go:build docker

package store_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/jackc/pgx/v5"
)

func TestUpgradeFrom0014PreservesSubagents(t *testing.T) {
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, `CREATE SCHEMA core; SET search_path = core;
        CREATE TABLE schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name() > "0014_session_storage.sql" {
			break
		}
		sql, err := os.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, strings.TrimSuffix(file.Name(), ".sql")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO sessions (id, title, kind, agent, name) VALUES ('home', 'Home', 'home', 'eika', 'home');
        INSERT INTO sessions (id, title, kind, agent, name, parent_session_id, state, brief, result)
        VALUES ('child', 'Plumber', 'subagent', 'researcher', 'plumber', 'home', 'waiting', 'saved brief', 'saved result');
        INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES ('entry', 'child', 1, 'user', '{"role":"user","content":"saved"}');
        UPDATE sessions SET head_entry_id = 'entry' WHERE id = 'child'`); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	child, err := st.Subagent(ctx, "home", "plumber")
	if err != nil || child.WaitingReason != "" || child.State != store.SessionWaiting || child.Brief != "saved brief" || child.Result != "saved result" || child.HeadEntryID != "entry" {
		t.Fatalf("upgraded child: %+v %v", child, err)
	}
	if _, err := st.SetSubagentState(ctx, child.ID, store.SessionWaiting, "test wait"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	child, err = st.Subagent(ctx, "home", "plumber")
	if err != nil || child.WaitingReason != "test wait" {
		t.Fatalf("reopened child: %+v %v", child, err)
	}
}
