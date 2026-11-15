//go:build docker

package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestSubagentStatesAndParentScope(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	home, err := st.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child, err := st.CreateSession(ctx, store.Session{Kind: store.SessionSubagent, ParentSessionID: home.ID, Agent: "researcher", Name: "plumber", Brief: "original", Result: "saved report", State: store.SessionRunning})
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.Subagent(ctx, home.ID, "plumber")
	if err != nil || created.State != store.SessionRunning || created.Brief != "original" {
		t.Fatalf("created child: %+v %v", created, err)
	}
	for _, state := range []store.SessionState{store.SessionRunning, store.SessionWaiting, store.SessionIdle, store.SessionFailed, store.SessionCancelled} {
		reason := ""
		if state == store.SessionWaiting {
			reason = "test wait"
		}
		got, err := st.SetSubagentState(ctx, child.ID, state, reason)
		if err != nil || got.State != state || got.WaitingReason != reason || got.Brief != child.Brief || got.Result != child.Result || got.LastActiveAt.Before(child.LastActiveAt) {
			t.Fatalf("state: %+v %v", got, err)
		}
		same, err := st.SetSubagentState(ctx, child.ID, state, reason)
		if err != nil || !reflect.DeepEqual(same, got) {
			t.Fatalf("repeated write: %+v %v", same, err)
		}
		read, err := st.Subagent(ctx, home.ID, "plumber")
		if err != nil || !reflect.DeepEqual(read, got) {
			t.Fatalf("read: %+v %v", read, err)
		}
	}
	for _, input := range []struct {
		state  store.SessionState
		reason string
	}{{"unknown", ""}, {store.SessionIdle, "invalid reason"}} {
		if _, err := st.SetSubagentState(ctx, child.ID, input.state, input.reason); err == nil {
			t.Fatal("accepted invalid state")
		}
	}
	if _, err := st.SetSubagentState(ctx, home.ID, store.SessionRunning, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("updated home: %v", err)
	}
	if _, err := st.Subagent(ctx, "other", "plumber"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wrong parent: %v", err)
	}
	rows, err := st.Subagents(ctx, home.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != child.ID {
		t.Fatalf("board: %+v %v", rows, err)
	}
}

func TestSubagentResultReplacesTheLastReportAndKeepsState(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	home, err := st.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child, err := st.CreateSession(ctx, store.Session{Kind: store.SessionSubagent, ParentSessionID: home.ID, Agent: "worker", Name: "plumber", Brief: "brief", State: store.SessionRunning})
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []string{"first report", "second report\nwith detail"} {
		got, err := st.SetSubagentResult(ctx, child.ID, report)
		if err != nil || got.Result != report || got.State != store.SessionRunning || got.Brief != "brief" {
			t.Fatalf("result: %+v %v", got, err)
		}
		if read, err := st.Subagent(ctx, home.ID, "plumber"); err != nil || read.Result != report {
			t.Fatalf("read: %+v %v", read, err)
		}
	}
	if _, err := st.SetSubagentResult(ctx, home.ID, "not a subagent"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("set result of home: %v", err)
	}
}
