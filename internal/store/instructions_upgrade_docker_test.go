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

// Migration 0013 adds instructions without changing existing records.
func TestUpgradeFrom0012AddsInstructions(t *testing.T) {
	ctx := t.Context()
	databaseURL := storetest.URL(t)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	names := []string{
		"0001_baseline", "0002_remove_session_forks", "0003_remove_computer_filing", "0004_provider_probe",
		"0005_model_request_usage", "0006_model_calls", "0007_provider_api", "0008_model_classes", "0009_call_phases", "0010_entry_payload_json", "0011_agents", "0012_projects",
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
		`INSERT INTO projects (id, slug, name, directory) VALUES ('project', 'saved', 'Saved', '/home/eika/saved')`,
		`INSERT INTO agents (name, prompt) VALUES ('eika', 'saved prompt')`,
		`UPDATE profiles SET chat_prompt = 'old prompt', instructions = 'old rules' WHERE name = 'Default'`,
		`INSERT INTO settings (key, value) VALUES ('default_model', '"old-model"')`,
		`CREATE TABLE schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
		`INSERT INTO schema_migrations (version) VALUES `+applied,
		`INSERT INTO computers (id, name, state) VALUES ('c', 'old', 'running')`,
		`INSERT INTO sessions (id, computer_id, title) VALUES ('s', 'c', 'old')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES
		   ('e1', 's', 1, 'user', '{"role": "user", "content": "hi"}')`,
	)
	for _, stmt := range statements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("set up the 0012 database: %v\n%s", err, stmt)
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

	profiles, err := st.Profiles(ctx)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles: %v, %v", profiles, err)
	}
	if profiles[0].ChatPrompt == nil || *profiles[0].ChatPrompt != "old prompt" || profiles[0].Instructions == nil || *profiles[0].Instructions != "old rules" {
		t.Fatal(profiles[0])
	}
	setting, err := st.Setting(ctx, "default_model")
	if err != nil || string(setting.Value) != `"old-model"` {
		t.Fatalf("setting: %s, %v", setting.Value, err)
	}

	if got, err := st.AgentOverrides(ctx); err != nil || got["eika"].Prompt == nil || *got["eika"].Prompt != "saved prompt" {
		t.Fatalf("agent overrides: %v %v", got, err)
	}
	if got, err := st.Projects(ctx); err != nil || len(got) != 1 {
		t.Fatalf("projects: %v %v", got, err)
	}
	if got, err := st.Instructions(ctx); err != nil || got != (store.GlobalInstructions{}) {
		t.Fatalf("initial instructions: %+v %v", got, err)
	}
	if got, err := st.ProjectInstructions(ctx, "project"); err != nil || got != "" {
		t.Fatalf("initial project instructions: %q %v", got, err)
	}
	if err := st.SetInstructions(ctx, store.GlobalInstructions{AboutMe: "Saved about me", Rules: "Saved rules"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectInstructions(ctx, "project", "Saved project rules"); err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, "upgrade", store.ProjectFields{Name: "Upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	reopened, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Project(ctx, p.ID)
	if err != nil || got.Slug != "upgrade" || got.Directory != "/home/eika/projects/upgrade" {
		t.Fatalf("reopened: %+v %v", got, err)
	}
	if got, err := reopened.Instructions(ctx); err != nil || got.AboutMe != "Saved about me" || got.Rules != "Saved rules" {
		t.Fatalf("persist global: %+v %v", got, err)
	}
	if got, err := reopened.ProjectInstructions(ctx, "project"); err != nil || got != "Saved project rules" {
		t.Fatalf("persist project: %q %v", got, err)
	}
	if got, err := reopened.Project(ctx, "project"); err != nil || got.Name != "Saved" || got.Directory != "/home/eika/saved" {
		t.Fatalf("old project: %+v %v", got, err)
	}
	if _, err := reopened.Entry(ctx, "e1"); err != nil {
		t.Fatal(err)
	}
}
