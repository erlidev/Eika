//go:build docker

package store_test

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestRetireProfilesPreservesPopulatedHistory(t *testing.T) {
	ctx := t.Context()
	st, conn := upgradeFrom(t, "0020_main_model_class",
		`UPDATE profiles SET chat_prompt = 'first prompt', tools = ARRAY['web_search'], sampling = '{"temperature":0.3}' WHERE name = 'Default'`,
		`INSERT INTO profiles (id, name, instructions, tools) VALUES ('second', 'Second', 'second rules', ARRAY['mcp__docs__*'])`,
		`INSERT INTO settings (key, value) VALUES ('default_profile', '"second"')`,
		`INSERT INTO computers (id, name, state) VALUES ('computer', 'Work', 'stopped')`,
		`INSERT INTO sessions (id, title, kind, profile_id, overrides, tools) VALUES
          ('chat', 'Old chat', 'user', 'second', '{"instructions":"personal"}', ARRAY['web_search']),
          ('blank', '', 'user', NULL, NULL, NULL)`,
		`INSERT INTO sessions (id, title, kind, computer_id, profile_id) VALUES
          ('work', 'Old work', 'user', 'computer', (SELECT id FROM profiles WHERE name = 'Default'))`,
		`INSERT INTO sessions (id, title, kind, owner, name, agent) VALUES ('home', 'Home', 'home', 'local', 'home', 'eika')`,
		`INSERT INTO session_entries (id, session_id, parent_id, seq, kind, payload) VALUES
          ('u', 'chat', NULL, 1, 'user', '{"role":"user","content":"saved"}'),
          ('old', 'chat', 'u', 2, 'system', '{"text":"old note"}'),
          ('answer', 'chat', 'old', 3, 'assistant', '{"role":"assistant","content":"answer"}'),
          ('branch', 'chat', 'u', 4, 'tool_call', '{"name":"old tool"}')`,
		`UPDATE sessions SET head_entry_id = 'answer' WHERE id = 'chat'`,
	)
	chat, err := st.Session(ctx, "chat")
	if err != nil || chat.Kind != store.SessionRoutine || chat.Agent != "eika" || chat.Name != "Old chat" || chat.ComputerID != "" || chat.HeadEntryID != "answer" {
		t.Fatalf("chat = %+v, %v", chat, err)
	}
	work, err := st.Session(ctx, "work")
	if err != nil || work.Kind != store.SessionRoutine || work.ComputerID != "computer" || work.Name != "Old work" {
		t.Fatalf("work = %+v, %v", work, err)
	}
	blank, err := st.Session(ctx, "blank")
	if err != nil || blank.Name != "blank" || blank.Kind != store.SessionRoutine {
		t.Fatalf("blank = %+v, %v", blank, err)
	}
	home, err := st.HomeSession(ctx, store.LocalOwner, "home")
	if err != nil || home.ID != "home" {
		t.Fatalf("home = %+v, %v", home, err)
	}
	entries, err := st.Entries(ctx, "chat")
	if err != nil || len(entries) != 4 || entries[1].Kind != store.EntryKind("system") || string(entries[1].Payload) != `{"text":"old note"}` || entries[3].ParentID != "u" || entries[3].Kind != store.EntryKind("tool_call") {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	path, err := st.SessionPath(ctx, "chat")
	if err != nil || len(path) != 3 || path[2].ID != "answer" {
		t.Fatalf("path = %+v, %v", path, err)
	}
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM core.retired_profiles WHERE (name = 'Default' AND chat_prompt = 'first prompt' AND sampling->>'temperature' = '0.3') OR (id = 'second' AND instructions = 'second rules' AND tools = ARRAY['mcp__docs__*'])`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("saved profiles = %d, %v", count, err)
	}
	var profile, original, instructions, defaultID string
	var tools []string
	if err := conn.QueryRow(ctx, `SELECT profile_id, original_kind, overrides->>'instructions', tools FROM core.retired_session_profiles WHERE session_id = 'chat'`).Scan(&profile, &original, &instructions, &tools); err != nil || profile != "second" || original != "user" || instructions != "personal" || len(tools) != 1 || tools[0] != "web_search" {
		t.Fatalf("saved session settings = %s %s %s %v, %v", profile, original, instructions, tools, err)
	}
	if err := conn.QueryRow(ctx, `SELECT value #>> '{}' FROM core.retired_profile_defaults WHERE key = 'default_profile'`).Scan(&defaultID); err != nil || defaultID != "second" {
		t.Fatalf("saved default = %q, %v", defaultID, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'sessions' AND column_name IN ('profile_id', 'overrides', 'tools')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old columns = %d, %v", count, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'profiles'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("profiles table = %d, %v", count, err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO core.session_entries (id, session_id, seq, kind, payload) VALUES ('new-old', 'chat', 5, 'event', '{}')`); err == nil {
		t.Fatal("obsolete entry kind was accepted")
	}
	if _, err := conn.Exec(ctx, `INSERT INTO core.sessions (id, kind) VALUES ('new-chat', 'user')`); err == nil {
		t.Fatal("direct session kind was accepted")
	}
}
