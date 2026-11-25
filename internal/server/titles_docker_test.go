//go:build docker

package server_test

import (
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

// assignClasses writes the model_classes setting.
func (a *api) assignClasses(t *testing.T, classes map[string]any) {
	t.Helper()
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"model_classes": classes,
	}), 200)
}

type sessionResponseWire struct {
	Session sessionWire `json:"session"`
}

func TestUntitledRetainedSessionGetsATitle(t *testing.T) {
	a := newAPI(t)
	sess, err := a.store.CreateSession(t.Context(), store.Session{Kind: store.SessionRoutine, Agent: "eika", Name: "old-chat", Title: "New chat", Untitled: true})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{"name": "title", "base_url": "https://title.test/v1"}), 201)
	decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{"provider_id": endpoint.ID, "name": "titler", "model": "vendor/titler", "context_window": 8192, "max_output": 512}), 201)
	titler := providertest.New(providertest.Text(`"Fix the login loop."`))
	a.serveAt("https://title.test/v1", titler)
	a.assignClasses(t, map[string]any{"fast": "titler"})
	events := a.Bus().Subscribe(event.TopicGlobal)
	defer events.Close()
	a.script(providertest.Text("done"))
	a.postMessage(t, sess.ID, "fix the login loop", "", 202)
	var title event.SessionTitle
	waitFor(t, "session title", func() bool {
		select {
		case e := <-events.Events():
			if e.Type == event.TypeSessionTitle {
				if err := e.DecodePayload(&title); err != nil {
					t.Fatal(err)
				}
				return true
			}
		default:
		}
		return false
	})
	if title.SessionID != sess.ID || title.Title != "Fix the login loop" {
		t.Fatalf("title event = %+v", title)
	}
	got, err := a.store.Session(t.Context(), sess.ID)
	if err != nil || got.Title != title.Title || got.Untitled {
		t.Fatalf("session = %+v, %v", got, err)
	}
}
