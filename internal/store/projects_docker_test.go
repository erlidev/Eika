//go:build docker

package store_test

import (
	"errors"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestProjectStorage(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	p, err := st.CreateProject(ctx, "taxes", store.ProjectFields{Name: "Taxes"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "active" || p.Directory != "/home/eika/projects/taxes" {
		t.Fatal(p)
	}
	if _, err := st.CreateProject(ctx, "taxes", store.ProjectFields{Name: "Duplicate"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := st.CreateProject(ctx, "bad", store.ProjectFields{Name: "Bad", Directory: "/a/../b"}); !errors.Is(err, store.ErrInvalidProject) {
		t.Fatalf("invalid create: %v", err)
	}
	updated, err := st.UpdateProject(ctx, p.ID, store.ProjectUpdate{Status: new("done"), Name: new("Renamed")})
	if err != nil || updated.Slug != p.Slug || updated.Directory != p.Directory || updated.Name != "Renamed" || updated.Status != "done" {
		t.Fatalf("update: %+v %v", updated, err)
	}
	// An invalid update changes nothing, not even the valid fields beside it.
	if _, err := st.UpdateProject(ctx, p.ID, store.ProjectUpdate{Directory: new("/a/../b"), Name: new("Lost")}); !errors.Is(err, store.ErrInvalidProject) {
		t.Fatal(err)
	}
	if saved, err := st.Project(ctx, p.ID); err != nil || saved.Name != "Renamed" || saved.Directory != p.Directory {
		t.Fatalf("invalid update changed row: %+v %v", saved, err)
	}
}

func TestProjectConcurrentUpdatesKeepBothFields(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	p, err := st.CreateProject(ctx, "shared", store.ProjectFields{Name: "Before"})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, update := range []store.ProjectUpdate{{Name: new("After")}, {Description: new("Saved")}} {
		go func() { <-start; _, err := st.UpdateProject(ctx, p.ID, update); results <- err }()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	got, err := st.Project(ctx, p.ID)
	if err != nil || got.Name != "After" || got.Description != "Saved" {
		t.Fatalf("concurrent updates: %+v %v", got, err)
	}
}
