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

// upgradeFrom0007 builds a database as migration 0007 left it, runs setup
// on it, and opens the store over it, which applies the migrations after.
func upgradeFrom0007(t *testing.T, setup ...string) *store.Store {
	t.Helper()
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	names := []string{
		"0001_baseline", "0002_remove_session_forks", "0003_remove_computer_filing",
		"0004_provider_probe", "0005_model_request_usage", "0006_model_calls", "0007_provider_api",
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
		`INSERT INTO schema_migrations (version) VALUES ('0001_baseline'), ('0002_remove_session_forks'), ('0003_remove_computer_filing'), ('0004_provider_probe'), ('0005_model_request_usage'), ('0006_model_calls'), ('0007_provider_api')`,
		`INSERT INTO providers (id, name, kind, base_url) VALUES ('p', 'bench', 'openai', 'http://m.invalid/v1')`,
		`INSERT INTO models (id, provider_id, name, model, context_window, max_output) VALUES ('m', 'p', 'small', 'vendor/small', 8192, 512)`,
	)
	statements = append(statements, setup...)
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("set up the 0007 database: %v\n%s", err, stmt)
		}
	}
	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// A model that exists before kinds is a chat model, and the model a user
// assigned the session title task becomes the fast class.
func TestUpgradeFrom0007CarriesTheTitleModelToTheFastClass(t *testing.T) {
	ctx := t.Context()
	st := upgradeFrom0007(t, `INSERT INTO settings (key, value) VALUES ('utility_models', '{"session_title": "small"}'), ('default_model', '"small"')`)

	m, err := st.Model(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != store.ModelKindChat {
		t.Errorf("kind after the upgrade = %q, want chat", m.Kind)
	}
	classes, err := st.Setting(ctx, "model_classes")
	if err != nil || string(classes.Value) != `{"fast": "small"}` {
		t.Errorf("model_classes = %s, %v, want the title model as fast", classes.Value, err)
	}
	if _, err := st.Setting(ctx, "utility_models"); err == nil {
		t.Error("utility_models is still stored after the upgrade")
	}
	if def, err := st.Setting(ctx, "default_model"); err != nil || string(def.Value) != `"small"` {
		t.Errorf("default_model = %s, %v, want it untouched", def.Value, err)
	}
}

// A user who assigned no title model has no class set, and neither a setting
// that was cleared nor one of the wrong shape makes a class.
func TestUpgradeFrom0007SetsNoClassWhenNoTitleModelWasAssigned(t *testing.T) {
	for name, value := range map[string]string{
		"an empty name":      `{"session_title": ""}`,
		"no task":            `{}`,
		"a null":             `null`,
		"a value of a shape": `"small"`,
	} {
		t.Run(name, func(t *testing.T) {
			st := upgradeFrom0007(t, `INSERT INTO settings (key, value) VALUES ('utility_models', '`+value+`')`)
			if _, err := st.Setting(t.Context(), "model_classes"); err == nil {
				t.Error("model_classes is set, want no class")
			}
			if _, err := st.Setting(t.Context(), "utility_models"); err == nil {
				t.Error("utility_models is still stored after the upgrade")
			}
		})
	}
}
