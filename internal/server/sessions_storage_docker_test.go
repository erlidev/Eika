//go:build docker

package server_test

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

func TestSessionStorageAPIAndReplay(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	home, err := a.store.CreateSession(ctx, store.Session{Kind: store.SessionHome, Name: "home", Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []store.EntryKind{store.KindPreamble, store.KindNotice} {
		text := "  exact <text> & café\nsecond line"
		entry, err := session.TextEntry(kind, text)
		if err != nil {
			t.Fatal(err)
		}
		entry.Channel = "web"
		saved, err := a.store.AppendEntry(ctx, home.ID, entry)
		if err != nil {
			t.Fatal(err)
		}
		body := decodeBody[struct {
			Session struct {
				Owner string `json:"owner"`
				Agent string `json:"agent"`
				Name  string `json:"name"`
				State string `json:"state"`
				Kind  string `json:"kind"`
			} `json:"session"`
			Head struct {
				Text    string `json:"text"`
				Channel string `json:"channel"`
			} `json:"head"`
		}](t, request(t, a.Server, "GET", "/api/sessions/"+home.ID, nil), 200)
		if body.Session.Owner != store.LocalOwner || body.Session.Agent != "eika" || body.Session.Name != "home" || body.Session.State != "idle" || body.Session.Kind != "home" || body.Head.Text != text || body.Head.Channel != "web" {
			t.Fatalf("session: %+v", body)
		}
		conn := stream(t, a.Server, "")
		send(t, conn, map[string]any{"type": "session.replay", "session_id": home.ID, "since": saved.ParentID})
		replayed := next(t, conn)
		var payload event.SessionMessage
		if err := replayed.DecodePayload(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.EntryID != saved.ID || payload.Channel != "web" || payload.Kind != string(kind) {
			t.Fatalf("replay: %+v", payload)
		}
		var note struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(payload.Message, &note); err != nil || note.Text != text {
			t.Fatalf("replay text: %+v %v", note, err)
		}
	}
}
