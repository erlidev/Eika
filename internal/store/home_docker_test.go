//go:build docker

package store_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestOnlyOneComputerIsHome(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	if _, err := st.CreateComputer(ctx, store.Computer{Name: "home", State: "running", Home: true}); err != nil {
		t.Fatalf("create the home computer: %v", err)
	}
	if _, err := st.CreateComputer(ctx, store.Computer{Name: "home too", State: "running", Home: true}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("a second home computer = %v, want ErrConflict", err)
	}
	// Any number of computers that are not home.
	for _, name := range []string{"a", "b"} {
		if _, err := st.CreateComputer(ctx, store.Computer{Name: name, State: "running"}); err != nil {
			t.Errorf("create %s: %v", name, err)
		}
	}
}

func TestEnsureHomeComputerRacingInsertsOnce(t *testing.T) {
	st := storetest.Open(t)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ids     = map[string]bool{}
		inserts int
	)
	for range 16 {
		wg.Go(func() {
			home, created, err := st.EnsureHomeComputer(t.Context(), store.Computer{Name: "home", State: "creating"})
			if err != nil {
				t.Errorf("EnsureHomeComputer: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			ids[home.ID] = true
			if created {
				inserts++
			}
		})
	}
	wg.Wait()
	if len(ids) != 1 || inserts != 1 {
		t.Errorf("racing ensures saw %d ids and inserted %d rows, want 1 and 1", len(ids), inserts)
	}
}

func TestEnsureHomeSessionRacingAndRestart(t *testing.T) {
	st := storetest.Open(t)
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	for range 16 {
		wg.Go(func() {
			home, err := st.EnsureHomeSession(t.Context())
			if err != nil {
				t.Error(err)
				return
			}
			ids <- home.ID
		})
	}
	wg.Wait()
	close(ids)
	var id string
	for got := range ids {
		if id == "" {
			id = got
		}
		if got != id {
			t.Fatalf("home IDs differ: %s and %s", id, got)
		}
	}
	home, err := st.HomeSession(t.Context(), store.LocalOwner, "home")
	if err != nil || home.ID != id || home.Agent != "eika" {
		t.Fatalf("home = %+v, %v", home, err)
	}
	again, err := st.EnsureHomeSession(t.Context())
	if err != nil || again.ID != id {
		t.Fatalf("again = %+v, %v", again, err)
	}
}
