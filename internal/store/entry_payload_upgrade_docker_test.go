//go:build docker

package store_test

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
)

// Entries written while the payload was jsonb are still there, with the
// document they held, after the column becomes json; new ones keep their text.
func TestUpgradeFrom0009KeepsTheEntriesItHolds(t *testing.T) {
	ctx := t.Context()
	st, _ := upgradeFrom(t, "0009_call_phases",
		`INSERT INTO computers (id, name, state) VALUES ('c', 'old', 'running')`,
		`INSERT INTO sessions (id, computer_id, title) VALUES ('s', 'c', 'old')`,
		`INSERT INTO session_entries (id, session_id, seq, kind, payload) VALUES
		   ('e1', 's', 1, 'user', '{"role": "user", "content": "hi"}')`,
	)

	e, err := st.Entry(ctx, "e1")
	if err != nil {
		t.Fatalf("read the entry written before the upgrade: %v", err)
	}
	if got, want := string(e.Payload), `{"role": "user", "content": "hi"}`; got != want {
		t.Errorf("old payload = %s, want %s", got, want)
	}
	added, err := st.AppendEntry(ctx, "s", store.Entry{Kind: store.KindUser, Payload: []byte(`{"content":"b","role":"user"}`)})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	if again, err := st.Entry(ctx, added.ID); err != nil || string(again.Payload) != `{"content":"b","role":"user"}` {
		t.Errorf("new payload = %s, %v; want the text it was written as", again.Payload, err)
	}
}
