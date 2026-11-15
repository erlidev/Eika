//go:build docker

package store_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

// A payload is read back as the text it was written as. A prompt cache reads
// the bytes of the conversation a request sends, and the conversation is read
// from these payloads at the start of every run; jsonb would write a document
// back with its keys reordered and a space after every colon.
func TestAnEntryPayloadComesBackAsTheTextItWasWrittenAs(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	cx, err := st.CreateComputer(ctx, store.Computer{Name: "payloads", State: "running"})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess, err := st.CreateSession(ctx, store.Session{ComputerID: cx.ID, Title: "payloads"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	payloads := []string{
		`{"role":"assistant","tool_calls":[{"id":"c","name":"echo","arguments":{"z":1,"a":2}}]}`,
		"{\n  \"role\": \"user\",\n  \"content\": \"a  b\"\n}",
		`{"role":"user","content":"café <&>","n":1.50,"e":1e5}`,
	}
	for _, payload := range payloads {
		if _, err := st.AppendEntry(ctx, sess.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(payload)}); err != nil {
			t.Fatalf("AppendEntry: %v", err)
		}
	}
	path, err := st.SessionPath(ctx, sess.ID)
	if err != nil {
		t.Fatalf("SessionPath: %v", err)
	}
	if len(path) != len(payloads) {
		t.Fatalf("path has %d entries, want %d", len(path), len(payloads))
	}
	for i, e := range path {
		if string(e.Payload) != payloads[i] {
			t.Errorf("entry %d came back as %s, want %s", i+1, e.Payload, payloads[i])
		}
	}
}

// A notice is undelivered until a user entry of its session, on any branch,
// lists it; another session's user entries never count.
func TestUndeliveredNoticesAreTheOnesNoUserEntryLists(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	home, err := st.CreateSession(ctx, store.Session{Kind: store.SessionHome, Name: "home", Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateSession(ctx, store.Session{Title: "other"})
	if err != nil {
		t.Fatal(err)
	}
	notice := func(text string) store.Entry {
		e, err := st.AppendEntry(ctx, home.ID, store.Entry{Kind: store.KindNotice, Payload: json.RawMessage(`{"text":"` + text + `"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	user := func(sessionID string, ids ...string) {
		raw, err := json.Marshal(map[string]any{"role": "user", "context": "<context></context>", "notices": ids})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AppendEntry(ctx, sessionID, store.Entry{Kind: store.KindUser, Payload: raw}); err != nil {
			t.Fatal(err)
		}
	}
	first, second, third := notice("one"), notice("two"), notice("three")
	user(home.ID, first.ID)
	user(other.ID, second.ID)
	// A user entry on a branch the head left still delivered its notices.
	if err := st.SetSessionHead(ctx, home.ID, third.ID); err != nil {
		t.Fatal(err)
	}
	got, err := st.UndeliveredNotices(ctx, home.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != second.ID || got[1].ID != third.ID {
		t.Fatalf("undelivered = %+v, want two then three", got)
	}
}

// An entry written once by its id is written once, however often it is
// written: a retry after a lost answer adds nothing and moves no head.
func TestAnEntryWrittenOnceByItsIDIsWrittenOnce(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	home, err := st.CreateSession(ctx, store.Session{Kind: store.SessionHome, Name: "home", Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	notice := store.Entry{ID: "notice-a", Kind: store.KindNotice, Payload: json.RawMessage(`{"text":"a"}`)}
	first, added, err := st.AppendEntryOnce(ctx, home.ID, notice)
	if err != nil || !added || first.ID != "notice-a" {
		t.Fatalf("first write = %+v, %v, %v", first, added, err)
	}
	later, err := st.AppendEntry(ctx, home.ID, store.Entry{ID: "ignored", Kind: store.KindNotice, Payload: json.RawMessage(`{"text":"b"}`)})
	if err != nil || later.ID == "ignored" {
		t.Fatalf("AppendEntry = %+v, %v; want a fresh id", later, err)
	}
	again, added, err := st.AppendEntryOnce(ctx, home.ID, notice)
	if err != nil || added || again.ID != "notice-a" || again.Seq != first.Seq {
		t.Fatalf("second write = %+v, %v, %v; want the first entry, not added", again, added, err)
	}
	entries, err := st.Entries(ctx, home.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("%d entries, want 2", len(entries))
	}
	if sess, err := st.Session(ctx, home.ID); err != nil || sess.HeadEntryID != later.ID {
		t.Errorf("head = %q, want %q: the repeated write moved it", sess.HeadEntryID, later.ID)
	}
	other, err := st.CreateSession(ctx, store.Session{Title: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AppendEntryOnce(ctx, other.ID, notice); !errors.Is(err, store.ErrConflict) {
		t.Errorf("the same id in another session = %v, want a conflict", err)
	}
}
