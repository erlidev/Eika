//go:build docker

package store_test

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestUpgradePreservesEntriesAndStoresTurnID(t *testing.T) {
	ctx := t.Context()
	st, _ := upgradeFrom(t, "0018_project_instruction_version",
		`INSERT INTO sessions (id, title) VALUES ('s', 'saved')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES ('old', 's', 1, 'user', '{"role":"user","content":"saved"}')`,
		`UPDATE sessions SET head_entry_id = 'old' WHERE id = 's'`,
	)
	old, err := st.Entry(ctx, "old")
	if err != nil || old.RunID != "" || string(old.Payload) != `{"role":"user","content":"saved"}` {
		t.Fatalf("old entry: %+v, %v", old, err)
	}
	added, err := st.AppendEntry(ctx, "s", store.Entry{Kind: store.KindAssistant, RunID: "turn-1", Payload: json.RawMessage(`{"role":"assistant","content":"answer"}`)})
	if err != nil {
		t.Fatal(err)
	}
	path, err := st.SessionPath(ctx, "s")
	if err != nil || len(path) != 2 || path[1].ID != added.ID || path[1].RunID != "turn-1" {
		t.Fatalf("path: %+v, %v", path, err)
	}
}
