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

func TestUpgradeFrom0015PreservesReportsAndNotices(t *testing.T) {
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
		if file.Name() > "0015_subagent_waiting.sql" {
			break
		}
		data, err := os.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(data)); err != nil {
			t.Fatalf("apply %s: %v", file.Name(), err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, strings.TrimSuffix(file.Name(), ".sql")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO sessions (id, title, kind, agent, name) VALUES ('home', 'Home', 'home', 'eika', 'home');
        INSERT INTO sessions (id, title, kind, agent, name, parent_session_id, result, state, waiting_reason)
            VALUES ('child', 'Child', 'subagent', 'researcher', 'child', 'home', 'kept report', 'waiting', 'a question');
        INSERT INTO session_entries (id, session_id, seq, kind, payload)
            VALUES ('notice-original', 'home', 1, 'notice', '{"text":"kept notice"}');
        UPDATE sessions SET head_entry_id = 'notice-original' WHERE id = 'home'`); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	child, err := st.Session(ctx, "child")
	if err != nil || child.Result != "kept report" || child.State != store.SessionWaiting || child.WaitingReason != "a question" || child.ReportNoticeID != "" {
		t.Fatalf("upgraded child = %+v, %v", child, err)
	}
	notices, err := st.UndeliveredNotices(ctx, "home")
	if err != nil || len(notices) != 1 || notices[0].ID != "notice-original" {
		t.Fatalf("upgraded notices = %+v, %v", notices, err)
	}
	child, err = st.PinReportNoticeID(ctx, child.ID, "notice-legacy")
	if err != nil || child.ReportNoticeID != "notice-legacy" || child.Result != "kept report" {
		t.Fatalf("pin upgraded report = %+v, %v", child, err)
	}
}
