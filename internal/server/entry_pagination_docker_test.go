//go:build docker

package server_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestEntryPaginationEndpoint(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	home, err := a.store.CreateSession(ctx, store.Session{Kind: store.SessionHome, Name: "home", Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.store.CreateSession(ctx, store.Session{Kind: store.SessionHome, Name: "other", Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	appendEntry := func(id string) store.Entry {
		t.Helper()
		entry, err := a.store.AppendEntry(ctx, id, store.Entry{Kind: store.KindUser, Payload: []byte(`{"role":"user","content":"test"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	foreign := appendEntry(other.ID)
	path := "/api/sessions/" + home.ID + "/entries"
	// A cursor from another session's history is as unknown as one that
	// never existed.
	for _, query := range []string{"before=" + store.NewID(), "before=" + foreign.ID} {
		rec := request(t, a.Server, http.MethodGet, path+"?"+query, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d: %s", query, rec.Code, rec.Body.String())
		}
	}
	type page struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
		Next string `json:"next_before"`
	}
	read := func(query string) page {
		t.Helper()
		return decodeBody[page](t, request(t, a.Server, http.MethodGet, path+query, nil), http.StatusOK)
	}
	if got := read(""); len(got.Entries) != 0 || got.Next != "" {
		t.Fatalf("empty: %+v", got)
	}
	var ids []string
	for i := range 4 {
		if i == 2 {
			e, added, err := a.store.AppendEntryOnce(ctx, home.ID, store.Entry{ID: "notice-original", Kind: store.KindNotice, Payload: []byte(`{"text":"legacy"}`)})
			if err != nil || !added {
				t.Fatalf("append notice: %v, added %v", err, added)
			}
			ids = append(ids, e.ID)
		} else {
			ids = append(ids, appendEntry(home.ID).ID)
		}
	}
	first := read("?limit=2")
	if len(first.Entries) != 2 || first.Entries[0].ID != ids[2] || first.Entries[1].ID != ids[3] || first.Next != ids[2] {
		t.Fatalf("first: %+v", first)
	}
	done := make(chan error, 1)
	go func() {
		_, err := a.store.AppendEntry(ctx, home.ID, store.Entry{Kind: store.KindUser, Payload: []byte(`{"role":"user","content":"later"}`)})
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	second := read("?limit=2&before=" + first.Next)
	if len(second.Entries) != 2 || second.Next != "" {
		t.Fatalf("last: %+v", second)
	}
	got := []string{second.Entries[0].ID, second.Entries[1].ID, first.Entries[0].ID, first.Entries[1].ID}
	if !reflect.DeepEqual(got, ids) {
		t.Fatalf("traversal = %v, want %v", got, ids)
	}
	empty := read("?before=" + ids[0])
	if len(empty.Entries) != 0 || empty.Next != "" {
		t.Fatalf("before first: %+v", empty)
	}
}
