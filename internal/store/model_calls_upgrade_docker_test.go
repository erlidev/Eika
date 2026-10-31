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

// A database at 0005 holds its model calls in model_requests, with the prompt
// and completion counts under their old names. The upgrade renames the table
// and the columns without touching a row, leaves every new column NULL, and
// leaves no object named after the old table behind.
func TestUpgradeFrom0005RenamesModelRequestsToModelCalls(t *testing.T) {
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	names := []string{"0001_baseline", "0002_remove_session_forks", "0003_remove_computer_filing", "0004_provider_probe", "0005_model_request_usage"}
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
		`INSERT INTO schema_migrations (version) VALUES ('0001_baseline'), ('0002_remove_session_forks'), ('0003_remove_computer_filing'), ('0004_provider_probe'), ('0005_model_request_usage')`,
		`INSERT INTO sessions (id, title) VALUES ('s', 's')`,
		`INSERT INTO runs (id, session_id, state) VALUES ('r', 's', 'done')`,
		`INSERT INTO model_requests (id, session_id, run_id, model, input_tokens, output_tokens, total_tokens, cached_tokens) VALUES ('stated', 's', 'r', 'm', 90, 100, 190, 86)`,
		`INSERT INTO model_requests (id, session_id, run_id, model) VALUES ('silent', 's', 'r', 'm')`,
	)
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("set up the 0005 database: %v\n%s", err, stmt)
		}
	}

	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	calls, err := st.ModelCalls(ctx, "s")
	if err != nil || len(calls) != 2 {
		t.Fatalf("ModelCalls = %+v, %v; want both rows kept", calls, err)
	}
	for _, c := range calls {
		switch c.ID {
		case "stated":
			if stated(c.PromptTokens) != 90 || stated(c.CompletionTokens) != 100 || stated(c.TotalTokens) != 190 || stated(c.CachedTokens) != 86 {
				t.Errorf("stated call = %+v, want its counts under their new names", c)
			}
		case "silent":
			if c.PromptTokens != nil || c.CompletionTokens != nil || c.CachedTokens != nil {
				t.Errorf("silent call = %+v, want its counts still NULL", c)
			}
		}
		unknown := c.PrefilledTokens != nil || c.ReasoningTokens != nil || c.ReasoningSource != "" ||
			c.PredictedPromptTokens != nil || c.Prediction != "" || c.Drift != nil || c.ContextWindow != nil ||
			c.Endpoint != "" || c.QueueMS != nil || c.Slot != nil || c.Class != "" || c.Priority != "" || c.Agent != ""
		if unknown {
			t.Errorf("%s: %+v, want every new column NULL for a call made before it existed", c.ID, c)
		}
	}

	var leftovers int
	const q = `SELECT (SELECT count(*) FROM pg_class WHERE relname LIKE 'model\_requests%' AND relnamespace = 'core'::regnamespace)
		+ (SELECT count(*) FROM pg_constraint WHERE conname LIKE 'model\_requests%' AND connamespace = 'core'::regnamespace)`
	if err := conn.QueryRow(ctx, q).Scan(&leftovers); err != nil {
		t.Fatalf("look for objects named after the old table: %v", err)
	}
	if leftovers != 0 {
		t.Errorf("%d tables, indexes or constraints are still named model_requests", leftovers)
	}
}
