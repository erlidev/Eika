//go:build docker

package store_test

import (
	"testing"

	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func memoryRecord(name, desc, body, state string) memory.Record {
	return memory.Record{ContentSHA: "sha-" + name, File: memory.File{
		Name: name, Description: desc, Type: "preference", Source: "session:s",
		Created: "2026-11-01T10:00:00Z", Updated: "2026-11-01T10:00:00Z",
		State: state, Body: body,
	}}
}

func TestMemorySearchRanksAndHidesArchived(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(ctx, storetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, r := range []memory.Record{
		memoryRecord("prefers-metric-units", "Use when giving measurements or recipes.", "The user wants kilograms and litres.", "active"),
		memoryRecord("cooking-notes", "Use when planning dinner for the user.", "Mentions metric once among many other recipe words.", "pending"),
		memoryRecord("old-metric", "Use when the old metric rule matters.", "Superseded metric rule.", "archived"),
	} {
		if err := st.UpsertMemory(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := st.SearchMemories(ctx, memory.Query{Text: "metric", Limit: 10}, 200)
	if err != nil || len(hits) != 2 || hits[0].Name != "prefers-metric-units" || hits[1].State != "pending" {
		t.Fatalf("hits = %+v, %v; want the name match first, pending marked, archived hidden", hits, err)
	}
	hits, err = st.SearchMemories(ctx, memory.Query{Text: "metric", IncludeArchived: true, Limit: 10}, 200)
	if err != nil || len(hits) != 3 {
		t.Fatalf("with archived: %+v, %v", hits, err)
	}
	if hits, err = st.SearchMemories(ctx, memory.Query{Text: "metric", Type: "fact", Limit: 10}, 5); err != nil || len(hits) != 0 {
		t.Fatalf("type filter: %+v, %v", hits, err)
	}
	// Upsert replaces the row and the search follows it.
	if err := st.UpsertMemory(ctx, memoryRecord("cooking-notes", "Use when planning dinner for the user.", "Now about imperial.", "active")); err != nil {
		t.Fatal(err)
	}
	if hits, _ = st.SearchMemories(ctx, memory.Query{Text: "imperial", Limit: 10}, 200); len(hits) != 1 {
		t.Fatalf("after replace: %+v", hits)
	}
}

func TestUpgradeAddsMemoriesWithoutLosingData(t *testing.T) {
	ctx := t.Context()
	st, conn := upgradeFrom(t, "0028_blocked_hosts", `INSERT INTO blocked_hosts (host) VALUES ('keep.example')`)
	if err := st.UpsertMemory(ctx, memoryRecord("first", "Use when testing the upgrade path.", "hello world", "active")); err != nil {
		t.Fatal(err)
	}
	var host string
	if err := conn.QueryRow(ctx, `SELECT host FROM blocked_hosts`).Scan(&host); err != nil || host != "keep.example" {
		t.Fatalf("blocked host: %q %v", host, err)
	}
	if hits, err := st.SearchMemories(ctx, memory.Query{Text: "hello", Limit: 5}, 200); err != nil || len(hits) != 1 {
		t.Fatalf("search: %+v %v", hits, err)
	}
}
