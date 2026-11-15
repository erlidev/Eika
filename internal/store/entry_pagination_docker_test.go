//go:build docker

package store_test

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestEntriesBefore(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	sess, err := st.CreateSession(ctx, store.Session{Kind: store.SessionHome, Agent: "eika", Name: "home"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.EntriesBefore(ctx, sess.ID, 0, 3)
	if err != nil || len(page) != 0 {
		t.Fatalf("empty page: %v %v", page, err)
	}
	var entries []store.Entry
	for i := 0; i < 5; i++ {
		e, err := st.AppendEntry(ctx, sess.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(`{"content":"test"}`)})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	page, err = st.EntriesBefore(ctx, sess.ID, 0, 3)
	if err != nil || len(page) != 3 || page[0].ID != entries[2].ID || page[2].ID != entries[4].ID {
		t.Fatalf("newest: %v %v", page, err)
	}
	if _, err = st.AppendEntry(ctx, sess.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(`{"content":"later"}`)}); err != nil {
		t.Fatal(err)
	}
	page, err = st.EntriesBefore(ctx, sess.ID, page[0].Seq, 3)
	if err != nil || len(page) != 2 || page[0].ID != entries[0].ID || page[1].ID != entries[1].ID {
		t.Fatalf("older: %v %v", page, err)
	}
}
