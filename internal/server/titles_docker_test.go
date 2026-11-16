//go:build docker

package server_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// utilityURL is the endpoint of the model the title tests assign the class.
const utilityURL = "https://utility.test/v1"

// addTitleModel adds a model on an endpoint of its own, whose calls p
// answers, and returns its name.
func (a *api) addTitleModel(t *testing.T, p provider.Provider) string {
	t.Helper()
	return a.addTitleModelOn(t, p, "")
}

// addTitleModelOn is addTitleModel on an endpoint whose backend is set to
// backend, such as openai for a hosted API.
func (a *api) addTitleModelOn(t *testing.T, p provider.Provider, backend string) string {
	t.Helper()
	utility := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{
		"name": "utility", "base_url": utilityURL,
	}), 201)
	if backend != "" {
		decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+utility.ID, map[string]any{
			"backend": backend,
		}), 200)
	}
	decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{
		"provider_id": utility.ID, "name": "titler", "model": "vendor/small",
		"context_window": 8192, "max_output": 512, "reasoning_effort": "high",
		"reasoning_efforts": []string{"none", "high"}, "thinking_switch": "thinking",
	}), 201)
	a.serveAt(utilityURL, p)
	return "titler"
}

// assignTitleModel assigns the fast class, which makes titles, a model, or
// none.
func (a *api) assignTitleModel(t *testing.T, name string) {
	t.Helper()
	a.assignClasses(t, map[string]any{"fast": name})
}

// assignClasses writes the model_classes setting.
func (a *api) assignClasses(t *testing.T, classes map[string]any) {
	t.Helper()
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"model_classes": classes,
	}), 200)
}

// sessionTitle reads the title of one session.
func (a *api) sessionTitle(t *testing.T, id string) string {
	t.Helper()
	return decodeBody[sessionResponseWire](t, request(t, a.Server, "GET", "/api/sessions/"+id, nil), 200).Session.Title
}

type sessionResponseWire struct {
	Session sessionWire `json:"session"`
}

func TestAnUntitledSessionIsNamedAfterItsFirstMessage(t *testing.T) {
	a := newAPI(t)
	titler := providertest.New(providertest.Text(`"Fix the login loop."`))
	a.assignTitleModel(t, a.addTitleModel(t, titler))
	events := a.Bus().Subscribe(event.TopicGlobal)
	defer events.Close()

	cx := a.home(t)
	untitled := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions", map[string]any{
		"computer_id": cx.ID,
	}), 201)
	if untitled.Title != "New session" {
		t.Errorf("title = %q, want the placeholder", untitled.Title)
	}
	chat := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions", map[string]any{"chat": true}), 201)
	if chat.Title != "New chat" {
		t.Errorf("chat title = %q, want the placeholder", chat.Title)
	}

	a.script(providertest.Text("on it"), providertest.Text("done"))
	a.postMessage(t, untitled.ID, "the login page loops forever, fix it", "", 202)

	var got event.SessionTitle
	waitFor(t, "a session.title event", func() bool {
		select {
		case e := <-events.Events():
			if e.Type == event.TypeSessionTitle {
				if err := e.DecodePayload(&got); err != nil {
					t.Fatal(err)
				}
				return true
			}
		default:
		}
		return false
	})
	if got != (event.SessionTitle{SessionID: untitled.ID, ComputerID: cx.ID, Title: "Fix the login loop"}) {
		t.Errorf("event = %+v", got)
	}
	if title := a.sessionTitle(t, untitled.ID); title != "Fix the login loop" {
		t.Errorf("stored title = %q", title)
	}

	reqs := titler.Requests()
	if len(reqs) != 1 {
		t.Fatalf("title requests = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if !strings.Contains(req.System, "Respond with only the session title") || req.Model != "vendor/small" ||
		len(req.Tools) != 0 || len(req.Messages) != 1 || req.Messages[0].Content != "the login page loops forever, fix it" {
		t.Errorf("title request = %+v", req)
	}
	if deref(req.Sampling.ReasoningEffort) != provider.EffortNone || req.ThinkingSwitch != provider.SwitchThinking {
		t.Errorf("title request effort = %q in %q, want thinking off", deref(req.Sampling.ReasoningEffort), req.ThinkingSwitch)
	}

	// A titled session is not named again.
	a.waitIdle(t, untitled.ID)
	a.postMessage(t, untitled.ID, "and the logout page", "", 202)
	a.waitIdle(t, untitled.ID)
	if titler.Calls() != 1 {
		t.Errorf("title requests = %d after a second run, want 1", titler.Calls())
	}
}
