//go:build docker

package store_test

import (
	"encoding/json"
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
	for _, state := range []store.SessionState{store.SessionIdle, store.SessionRunning, store.SessionWaiting, store.SessionFailed, store.SessionCancelled} {
		routine, err := st.CreateSession(ctx, store.Session{Kind: store.SessionRoutine, Agent: "worker", Name: string(state), State: state})
		if err != nil || routine.State != state {
			t.Fatalf("state %s: %+v %v", state, routine, err)
		}
	}
	title := "new title"
	edited, err := st.UpdateSession(ctx, saved.ID, store.SessionChanges{Title: &title})
	if err != nil || !edited.LastActiveAt.Equal(active) || edited.State != store.SessionWaiting {
		t.Fatalf("metadata edit: %+v %v", edited, err)
	}
	if err := st.DeleteSession(ctx, home.ID); err == nil {
		t.Fatal("deleted parent with child history")
	}
	payload := json.RawMessage(`{ "text": "keep <this> & spacing\n" }`)
	for _, kind := range []store.EntryKind{store.KindPreamble, store.KindNotice, store.KindUser, store.KindAssistant} {
		e, err := st.AppendEntry(ctx, home.ID, store.Entry{Kind: kind, Payload: payload, Channel: "web"})
		if err != nil {
			t.Fatal(err)
		}
		read, err := st.Entry(ctx, e.ID)
		if err != nil || !reflect.DeepEqual(e, read) || string(read.Payload) != string(payload) || read.Channel != "web" {
			t.Fatalf("entry: %+v %v", read, err)
		}
	}
	path, err := st.SessionPath(ctx, home.ID)
	if err != nil || len(path) != 4 || path[3].ParentID != path[2].ID || path[0].Channel != "web" {
		t.Fatalf("path: %+v %v", path, err)
	}
	entries, err := st.ChildEntries(ctx, home.ID, path[0].ID)
	if err != nil || len(entries) != 1 || entries[0].Channel != "web" {
		t.Fatalf("children: %+v %v", entries, err)
	}
}
