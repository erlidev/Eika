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

func TestEnsureHomeComputerFindsOrInsertsTheOne(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	first, created, err := st.EnsureHomeComputer(ctx, store.Computer{
		ID: "first", Name: "home", Image: "img", State: "creating",
		Sandbox: store.ComputerSandbox{PIDs: 4096},
	})
	if err != nil || !created {
		t.Fatalf("first ensure = %+v, created %v, %v; want it inserted", first, created, err)
	}
	if !first.Home || first.ID != "first" || first.Name != "home" || first.Sandbox.PIDs != 4096 {
		t.Errorf("first = %+v, want the row that was offered, marked home", first)
	}

	again, created, err := st.EnsureHomeComputer(ctx, store.Computer{ID: "second", Name: "other", State: "creating"})
	if err != nil || created {
		t.Fatalf("second ensure = %+v, created %v, %v; want the first row, not inserted", again, created, err)
	}
	if again.ID != "first" {
		t.Errorf("second ensure returned %q, want first", again.ID)
	}

	all, err := st.Computers(ctx)
	if err != nil || len(all) != 1 {
		t.Errorf("computers = %+v, %v; want the home computer alone", all, err)
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
