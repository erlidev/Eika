package session

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestOutlineMarksBothPreamblesNotResumable(t *testing.T) {
	entries := []store.Entry{
		{ID: "first", Kind: store.KindPreamble},
		{ID: "user", ParentID: "first", Kind: store.KindUser, Payload: []byte(`{"role":"user","content":"hi"}`)},
		{ID: "replacement", ParentID: "user", Kind: store.KindPreamble},
		{ID: "compact", ParentID: "replacement", Kind: store.KindCompaction},
	}
	got := resumableSet(entries)
	for id, want := range map[string]bool{"first": false, "user": true, "replacement": false, "compact": true} {
		if got[id] != want {
			t.Errorf("%s resumable = %v, want %v", id, got[id], want)
		}
	}
}
