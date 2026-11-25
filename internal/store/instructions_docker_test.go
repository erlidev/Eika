//go:build docker

package store_test

import (
	"errors"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestInstructionsStorageAndScopeIsolation(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	if got, err := st.Instructions(ctx); err != nil || got != (store.GlobalInstructions{}) {
		t.Fatalf("initial: %+v %v", got, err)
	}
	p, err := st.CreateProject(ctx, "one", store.ProjectFields{Name: "One"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProject(ctx, "two", store.ProjectFields{Name: "Two"})
	if err != nil {
		t.Fatal(err)
	}
	global := store.GlobalInstructions{AboutMe: "About me\n", Rules: "Global rules"}
	if err := st.SetInstructions(ctx, global); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectInstructions(ctx, p.ID, "# Project rules\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateProject(ctx, p.ID, store.ProjectUpdate{Name: new("Renamed")}); err != nil {
		t.Fatal(err)
	}
	if got, err := st.ProjectInstructions(ctx, p.ID); err != nil || got != "# Project rules\n" {
		t.Fatalf("metadata write: %q %v", got, err)
	}
	if got, err := st.ProjectInstructions(ctx, other.ID); err != nil || got != "" {
		t.Fatalf("other scope: %q %v", got, err)
	}
	if err := st.SetInstructions(ctx, store.GlobalInstructions{AboutMe: "lost", Rules: "\x00"}); !errors.Is(err, store.ErrInvalidInstructions) {
		t.Fatal(err)
	}
	if got, err := st.Instructions(ctx); err != nil || got != global {
		t.Fatalf("global: %+v %v", got, err)
	}
	if err := st.SetProjectInstructions(ctx, p.ID, "\x00"); !errors.Is(err, store.ErrInvalidInstructions) {
		t.Fatal(err)
	}
	if got, err := st.ProjectInstructions(ctx, p.ID); err != nil || got != "# Project rules\n" {
		t.Fatalf("invalid write: %q %v", got, err)
	}
}
