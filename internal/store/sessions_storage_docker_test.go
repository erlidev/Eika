//go:build docker

package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestSessionStorageIdentityAndEntries(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	home, err := st.CreateSession(ctx, store.Session{Kind: store.SessionHome, Agent: "eika", Name: "home"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.HomeSession(ctx, store.LocalOwner, "home")
	if err != nil || !reflect.DeepEqual(home, got) {
		t.Fatalf("home: %+v %v", got, err)
	}
	project, err := st.CreateProject(ctx, "test", store.ProjectFields{Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	active := time.Date(2026, 11, 14, 10, 0, 0, 123000, time.UTC)
	child := store.Session{Kind: store.SessionSubagent, Agent: "worker", Name: "plumber", ParentSessionID: home.ID,
		ProjectID: project.ID, Brief: "find a plumber", Result: "found one", State: store.SessionWaiting, LastActiveAt: active}
	saved, err := st.CreateSession(ctx, child)
	if err != nil {
		t.Fatal(err)
	}
	got, err = st.Session(ctx, saved.ID)
	if err != nil || !reflect.DeepEqual(saved, got) || got.ComputerID != "" || got.Brief != child.Brief || got.Result != child.Result || !got.LastActiveAt.Equal(active) {
		t.Fatalf("child: %+v %v", got, err)
	}
	cases := []struct {
		name  string
		input store.Session
	}{
		{"duplicate home", store.Session{Kind: store.SessionHome, Agent: "eika", Name: "home"}},
		{"duplicate child", child},
		{"missing parent", store.Session{Kind: store.SessionSubagent, Agent: "worker", Name: "orphan"}},
		{"unknown parent", store.Session{Kind: store.SessionSubagent, Agent: "worker", Name: "orphan", ParentSessionID: "missing"}},
		{"self parent", store.Session{ID: "self", Kind: store.SessionSubagent, Agent: "worker", Name: "self", ParentSessionID: "self"}},
		{"home parent", store.Session{Kind: store.SessionHome, Agent: "eika", Name: "other", ParentSessionID: home.ID}},
		{"empty name", store.Session{Kind: store.SessionHome, Agent: "eika"}},
		{"empty agent", store.Session{Kind: store.SessionHome, Name: "other"}},
		{"unknown state", store.Session{State: "done"}},
		{"unknown kind", store.Session{Kind: "unknown"}},
		{"unknown project", store.Session{ProjectID: "missing"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.CreateSession(ctx, tc.input); err == nil {
				t.Fatal("accepted invalid session")
			}
		})
	}
	other, err := st.CreateSession(ctx, store.Session{Kind: store.SessionHome, Agent: "eika", Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	child.ParentSessionID = other.ID
	if _, err := st.CreateSession(ctx, child); err != nil {
		t.Fatalf("same name under another parent: %v", err)
	}
	if _, err := st.CreateSession(ctx, store.Session{Kind: store.SessionHome, Owner: "other", Agent: "eika", Name: "home"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.HomeSession(ctx, store.LocalOwner, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing home: %v", err)
	}
	title := "new title"
	edited, err := st.UpdateSession(ctx, saved.ID, store.SessionChanges{Title: &title})
	if err != nil || !edited.LastActiveAt.Equal(active) || edited.State != store.SessionWaiting {
		t.Fatalf("metadata edit: %+v %v", edited, err)
	}
	if err := st.DeleteSession(ctx, home.ID); err == nil {
		t.Fatal("deleted parent with child history")
	}
}
