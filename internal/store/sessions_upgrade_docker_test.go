//go:build docker

package store_test

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
)

// Migration 0014 preserves populated legacy conversations and configuration.
func TestUpgradeFrom0013AddsSessionStorage(t *testing.T) {
	ctx := t.Context()
	st, _ := upgradeFrom(t, "0013_instructions",
		`INSERT INTO agents (name, prompt) VALUES ('eika', 'saved prompt')`,
		`INSERT INTO projects (id, slug, name, directory) VALUES ('project', 'saved', 'Saved', '/home/eika/saved')`,
		`INSERT INTO project_instructions (project_id, instructions) VALUES ('project', 'saved rules')`,
		`UPDATE profiles SET chat_prompt = 'old prompt', instructions = 'old rules' WHERE name = 'Default'`,
		`INSERT INTO settings (key, value) VALUES ('default_model', '"old-model"')`,
		`INSERT INTO sessions (id, title, updated_at, pinned, archived, tools, overrides)
           VALUES ('chat', 'old chat', '2026-11-01T12:00:00Z', true, true, ARRAY['web_search'], '{"instructions":"keep"}')`,
		`INSERT INTO computers (id, name, state) VALUES ('c', 'old', 'running')`,
		`INSERT INTO sessions (id, computer_id, title) VALUES ('s', 'c', 'old')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES
		   ('e1', 's', 1, 'user', '{"role": "user", "content": "hi"}')`,
		`INSERT INTO session_entries (id, session_id, parent_id, seq, kind, payload) VALUES
           ('e2', 's', 'e1', 2, 'assistant', '{"role":"assistant","content":"hello"}'),
           ('e3', 's', 'e1', 3, 'assistant', '{"role":"assistant","content":"branch"}')`,
		`UPDATE sessions SET head_entry_id = 'e2', profile_id = (SELECT id FROM profiles LIMIT 1) WHERE id = 's'`,
	)

	old, err := st.Session(ctx, "chat")
	if err != nil || old.Kind != store.SessionUser || old.Owner != store.LocalOwner || old.State != store.SessionIdle || old.ComputerID != "" || old.ParentSessionID != "" || old.Name != "" || !old.LastActiveAt.Equal(old.UpdatedAt) || !old.Pinned || !old.Archived || len(old.Tools) != 1 || old.Overrides.Instructions == nil || *old.Overrides.Instructions != "keep" {
		t.Fatalf("legacy chat: %+v %v", old, err)
	}
	attached, err := st.Session(ctx, "s")
	if err != nil || attached.ComputerID != "c" || attached.HeadEntryID != "e2" || attached.ProfileID == "" {
		t.Fatalf("attached: %+v %v", attached, err)
	}

	path, err := st.SessionPath(ctx, "s")
	if err != nil || len(path) != 2 || path[1].ID != "e2" || path[1].ParentID != "e1" {
		t.Fatalf("old path: %+v %v", path, err)
	}
	entries, err := st.Entries(ctx, "s")
	if err != nil || len(entries) != 3 || entries[2].ParentID != "e1" {
		t.Fatalf("old branches: %+v %v", entries, err)
	}
	if e := path[0]; e.Channel != "" || string(e.Payload) != `{"role": "user", "content": "hi"}` {
		t.Errorf("old entry = channel %q, payload %s; want no channel and the text it was written as", e.Channel, e.Payload)
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
	if got, err := st.Project(ctx, "project"); err != nil || got.Name != "Saved" || got.Directory != "/home/eika/saved" {
		t.Fatalf("old project: %+v %v", got, err)
	}
	if got, version, err := st.ProjectInstructionVersion(ctx, "project"); err != nil || got != "saved rules" || version != 1 {
		t.Fatalf("old project instructions: %q version %d: %v", got, version, err)
	}
}
