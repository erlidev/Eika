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
	if got, err := st.Projects(ctx); err != nil || len(got) != 0 {
		t.Fatalf("empty list: %v %v", got, err)
	}
	p, err := st.CreateProject(ctx, "taxes", store.ProjectFields{Name: "Taxes"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.Status != "active" || p.Directory != "/home/eika/projects/taxes" || p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Fatal(p)
	}
	if _, err := st.CreateProject(ctx, "taxes", store.ProjectFields{Name: "Duplicate"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	other, err := st.CreateProject(ctx, "aaa", store.ProjectFields{Name: "Other", Directory: "/home/eika/existing", Status: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"paused", "done", "archived", "active"} {
		updated, err := st.UpdateProject(ctx, p.ID, store.ProjectUpdate{Status: new(status), Name: new("Renamed"), Description: new("One line")})
		if err != nil {
			t.Fatal(err)
		}
		if updated.Slug != p.Slug || updated.Directory != p.Directory || updated.Name != "Renamed" || updated.Description != "One line" || updated.Status != status || !updated.CreatedAt.Equal(p.CreatedAt) || updated.UpdatedAt.Before(p.UpdatedAt) {
			t.Fatal(updated)
		}
	}
	if _, err := st.UpdateProject(ctx, p.ID, store.ProjectUpdate{Directory: new("/a/../b"), Name: new("Lost")}); !errors.Is(err, store.ErrInvalidProject) {
		t.Fatal(err)
	}
	saved, err := st.Project(ctx, p.ID)
	if err != nil || saved.Name != "Renamed" {
		t.Fatalf("invalid update changed row: %+v %v", saved, err)
	}
	saved, err = st.UpdateProject(ctx, p.ID, store.ProjectUpdate{Description: new(""), Directory: new("/tmp/new")})
	if err != nil || saved.Description != "" || saved.Directory != "/tmp/new" {
		t.Fatalf("clear description: %+v %v", saved, err)
	}
	all, err := st.Projects(ctx)
	if err != nil || len(all) != 2 || all[0].ID != other.ID || all[1].ID != p.ID {
		t.Fatalf("ordered list: %v %v", all, err)
	}
	if _, err := st.Project(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := st.UpdateProject(ctx, "missing", store.ProjectUpdate{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	for _, f := range []store.ProjectFields{{Name: ""}, {Name: "Bad", Status: "bad"}, {Name: "Bad", Directory: "/a/../b"}} {
		if _, err := st.CreateProject(ctx, "bad", f); !errors.Is(err, store.ErrInvalidProject) {
			t.Fatal(err)
		}
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
