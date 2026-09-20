//go:build docker

package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestSearchKeys(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	if _, err := st.SearchKey(ctx, "exa"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SearchKey before any = %v, want ErrNotFound", err)
	}
	for _, key := range []string{"sealed-1", "sealed-2"} {
		if err := st.SetSearchKey(ctx, "exa", []byte(key)); err != nil {
			t.Fatalf("SetSearchKey: %v", err)
		}
	}
	if err := st.SetSearchKey(ctx, "brave", []byte("b")); err != nil {
		t.Fatal(err)
	}
	if k, err := st.SearchKey(ctx, "exa"); err != nil || string(k.Key) != "sealed-2" || k.UpdatedAt.IsZero() {
		t.Errorf("SearchKey = %+v, %v; want the replacement", k, err)
	}
	keys, err := st.SearchKeys(ctx)
	if err != nil || len(keys) != 2 || keys[0].Name != "brave" || keys[1].Name != "exa" {
		t.Errorf("SearchKeys = %+v, %v", keys, err)
	}
	for range 2 {
		if err := st.DeleteSearchKey(ctx, "exa"); err != nil {
			t.Errorf("DeleteSearchKey: %v", err)
		}
	}
	if _, err := st.SearchKey(ctx, "exa"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SearchKey after delete = %v", err)
	}
}

func TestSearchUsage(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	until := time.Date(2026, 9, 18, 13, 0, 0, 0, time.UTC)

	for _, u := range []store.SearchUsage{
		{Name: "exa", Day: "2026-09-18", DayUsed: 1, Month: "2026-09", MonthUsed: 1},
		{Name: "exa", Day: "2026-09-18", DayUsed: 2, Month: "2026-09", MonthUsed: 7, CooldownUntil: until, FailStreak: 1},
		{Name: "brave", Day: "2026-09-18", Month: "2026-09"},
	} {
		if err := st.SetSearchUsage(ctx, u); err != nil {
			t.Fatalf("SetSearchUsage: %v", err)
		}
	}
	got, err := st.SearchUsage(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("SearchUsage = %+v, %v", got, err)
	}
	if got[0].Name != "brave" || !got[0].CooldownUntil.IsZero() {
		t.Errorf("brave = %+v", got[0])
	}
	want := store.SearchUsage{Name: "exa", Day: "2026-09-18", DayUsed: 2, Month: "2026-09", MonthUsed: 7, CooldownUntil: until, FailStreak: 1}
	if got[1] != want {
		t.Errorf("exa = %+v, want %+v", got[1], want)
	}
}
