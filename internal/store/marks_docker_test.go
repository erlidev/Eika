//go:build docker

package store_test

import (
	"testing"
	"time"

	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestPruneMarksDropsOnlyTheOldOnes(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	now := time.Now()
	for _, m := range []sentry.Mark{
		{Session: "s1", Run: "old", Kind: sentry.MarkWatched, Value: "old.example", At: now.Add(-31 * 24 * time.Hour)},
		{Session: "s1", Run: "new", Kind: sentry.MarkWatched, Value: "new.example", At: now.Add(-29 * 24 * time.Hour)},
	} {
		if err := st.AppendMark(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.PruneMarks(ctx, now.Add(-30*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("PruneMarks = %d, %v; want 1", n, err)
	}
	if hosts, err := st.WatchedHosts(ctx, "s1"); err != nil || len(hosts) != 1 || hosts[0] != "new.example" {
		t.Errorf("watched hosts = %v, %v; want only new.example", hosts, err)
	}
}
