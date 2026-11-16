//go:build docker

package store_test

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestUpgradeFrom0014PreservesSubagents(t *testing.T) {
	ctx := t.Context()
	st, _ := upgradeFrom(t, "0014_session_storage",
		`INSERT INTO sessions (id, title, kind, agent, name) VALUES ('home', 'Home', 'home', 'eika', 'home')`,
		`INSERT INTO sessions (id, title, kind, agent, name, parent_session_id, state, brief, result)
		    VALUES ('child', 'Plumber', 'subagent', 'researcher', 'plumber', 'home', 'waiting', 'saved brief', 'saved result')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES ('entry', 'child', 1, 'user', '{"role":"user","content":"saved"}')`,
		`UPDATE sessions SET head_entry_id = 'entry' WHERE id = 'child'`,
	)
	child, err := st.Subagent(ctx, "home", "plumber")
	if err != nil || child.WaitingReason != "" || child.State != store.SessionWaiting || child.Brief != "saved brief" || child.Result != "saved result" || child.HeadEntryID != "entry" {
		t.Fatalf("upgraded child: %+v %v", child, err)
	}
	// The waiting reason 0015 added can be set on a child that was waiting before it.
	if child, err := st.SetSubagentState(ctx, child.ID, store.SessionWaiting, "test wait"); err != nil || child.WaitingReason != "test wait" {
		t.Fatalf("set waiting reason: %+v %v", child, err)
	}
}
