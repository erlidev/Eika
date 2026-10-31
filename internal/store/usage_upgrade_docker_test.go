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

// A database at 0004 stored 0 for every count an endpoint did not state. The
// upgrade makes those NULL, since no call has an empty prompt, keeps every
// count that was stated, and leaves a stored cached count to be added.
func TestUpgradeFrom0004ReadsCallsThatStatedNothingAsNull(t *testing.T) {
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	names := []string{"0001_baseline", "0002_remove_session_forks", "0003_remove_computer_filing", "0004_provider_probe"}
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
		`INSERT INTO schema_migrations (version) VALUES ('0001_baseline'), ('0002_remove_session_forks'), ('0003_remove_computer_filing'), ('0004_provider_probe')`,
		`INSERT INTO sessions (id, title) VALUES ('s', 's')`,
		`INSERT INTO runs (id, session_id, state) VALUES ('r', 's', 'done')`,
		`INSERT INTO model_requests (id, session_id, run_id, model, input_tokens, output_tokens, total_tokens) VALUES ('stated', 's', 'r', 'm', 90, 100, 190)`,
		`INSERT INTO model_requests (id, session_id, run_id, model, input_tokens, output_tokens, total_tokens) VALUES ('zero-output', 's', 'r', 'm', 90, 0, 90)`,
		`INSERT INTO model_requests (id, session_id, run_id, model) VALUES ('silent', 's', 'r', 'm')`,
	)
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("set up the 0004 database: %v\n%s", err, stmt)
		}
	}

	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	type counts struct{ in, out, total, cached int }
	want := map[string]counts{
		"stated":      {90, 100, 190, -1},
		"zero-output": {90, 0, 90, -1},
		"silent":      {-1, -1, -1, -1},
	}
	records, err := st.ModelRequests(ctx, "s")
	if err != nil || len(records) != len(want) {
		t.Fatalf("ModelRequests = %+v, %v; want every record kept", records, err)
	}
	for _, r := range records {
		got := counts{stated(r.InputTokens), stated(r.OutputTokens), stated(r.TotalTokens), stated(r.CachedTokens)}
		if got != want[r.ID] {
			t.Errorf("%s: in/out/total/cached = %+v, want %+v (-1 is NULL)", r.ID, got, want[r.ID])
		}
	}
}
