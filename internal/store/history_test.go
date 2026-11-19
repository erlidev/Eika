package store_test

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestHistoryStateFoldsCompactions(t *testing.T) {
	message := func(n int64) store.Entry { return store.Entry{Seq: n, Kind: store.KindUser} }
	compact := func(n int64, kept int) store.Entry {
		b, _ := json.Marshal(map[string]int{"kept": kept})
		return store.Entry{Seq: n, Kind: store.KindCompaction, Payload: b}
	}
	path := []store.Entry{message(1), message(2), message(3), compact(4, 1), message(5), message(6), compact(7, 1), message(8)}
	state, err := store.HistoryState(path, -1)
	if err != nil || state.FirstCompacted != 1 || state.LastCompacted != 5 || state.FirstKept != 6 || state.Compactions != 2 {
		t.Fatalf("state = %+v, %v", state, err)
	}
	state, err = store.HistoryState(path, 1)
	if err != nil || state.LastCompacted != 6 || state.FirstKept != 8 || state.Compactions != 3 {
		t.Fatalf("pending state = %+v, %v", state, err)
	}
}
