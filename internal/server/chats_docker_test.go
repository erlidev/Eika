//go:build docker

package server_test

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// chatWire is a session as a chat reports it: no computer, and the tools its
// next run offers.
type chatWire struct {
	ID         string   `json:"id"`
	ComputerID *string  `json:"computer_id"`
	Title      string   `json:"title"`
	Kind       string   `json:"kind"`
	Tools      []string `json:"tools"`
}

type chatsWire struct {
	Sessions []chatWire `json:"sessions"`
}

type toolsWire struct {
	Tools []struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		NeedsComputer bool   `json:"needs_computer"`
	} `json:"tools"`
}

// newChat opens a chat.
func (a *api) newChat(t *testing.T, title string) chatWire {
	t.Helper()
	rec := request(t, a.Server, "POST", "/api/sessions", map[string]any{"chat": true, "title": title})
	return decodeBody[chatWire](t, rec, 201)
}

// standaloneTools are the built-in tools a chat may offer.
var standaloneTools = []string{"ask_user", "web_fetch", "web_search"}

// refused checks that a request was answered with an error status.
func refused(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

func TestChatRoutes(t *testing.T) {
	a := newAPI(t)
	cx := a.home(t)
	work := a.newSession(t, cx.ID)

	chat := a.newChat(t, "a question")
	if chat.ComputerID != nil {
		t.Errorf("chat computer_id = %q, want it absent", *chat.ComputerID)
	}
	if !slices.Equal(chat.Tools, standaloneTools) {
		t.Errorf("new chat tools = %v, want every standalone tool %v", chat.Tools, standaloneTools)
	}

	// Leaving the computer out is a missing computer, never a chat nobody
	// asked for.
	refused(t, request(t, a.Server, "POST", "/api/sessions", map[string]any{"title": "x"}), 404)

	chats := decodeBody[chatsWire](t, request(t, a.Server, "GET", "/api/sessions?chats=true", nil), 200)
	if len(chats.Sessions) != 1 || chats.Sessions[0].ID != chat.ID {
		t.Errorf("chats = %+v, want the one chat", chats.Sessions)
	}
	inComputer := decodeBody[chatsWire](t, request(t, a.Server, "GET", "/api/sessions?computer_id="+cx.ID, nil), 200)
	if len(inComputer.Sessions) != 1 || inComputer.Sessions[0].ID != work.ID {
		t.Errorf("computer sessions = %+v, want the computer's own", inComputer.Sessions)
	}
	if len(inComputer.Sessions[0].Tools) != 7 {
		t.Errorf("computer session tools = %v, want all 7", inComputer.Sessions[0].Tools)
	}

	tools := decodeBody[toolsWire](t, request(t, a.Server, "GET", "/api/tools", nil), 200)
	var free []string
	for _, tl := range tools.Tools {
		if tl.Description == "" {
			t.Errorf("tool %s has no description", tl.Name)
		}
		if !tl.NeedsComputer {
			free = append(free, tl.Name)
		}
	}
	if len(tools.Tools) != 7 || !slices.Equal(free, standaloneTools) {
		t.Errorf("tools = %+v, want 7 with %v standalone", tools.Tools, standaloneTools)
	}
}

// A chat's run has no computer and offers the model exactly the tools the
// user left on. A call to anything else, bash included, is refused as a tool
// the run does not have, so the choice holds even against a model that
// ignores the tool list it was sent.
func TestChatRunOffersOnlyItsChosenTools(t *testing.T) {
	a := newAPI(t)
	chat := a.newChat(t, "a question")
	decodeBody[chatWire](t, request(t, a.Server, "PUT", "/api/sessions/"+chat.ID+"/tools",
		map[string]any{"tools": []string{"web_search"}}), 200)

	p := a.script(
		providertest.Calls("",
			providertest.Call("c1", "web_search", map[string]any{"query": "tokio"}),
			providertest.Call("c2", "bash", map[string]any{"command": "ls"}),
			providertest.Call("c3", "web_fetch", map[string]any{"url": "https://example.com/guide.md"}),
		),
		providertest.Text("Tokio is an async runtime."),
	)
	a.postMessage(t, chat.ID, "what is tokio?", "", 202)
	state := a.waitIdle(t, chat.ID)
	if state.Run == nil || state.Run.State != "done" {
		t.Fatalf("run = %+v, want done", state.Run)
	}

	requests := p.Requests()
	if len(requests) != 2 {
		t.Fatalf("model calls = %d, want 2", len(requests))
	}
	var offered []string
	for _, def := range requests[0].Tools {
		offered = append(offered, def.Name)
	}
	if !slices.Equal(offered, []string{"web_search"}) {
		t.Errorf("tools offered = %v, want web_search alone", offered)
	}

	// The loop mints its own id for each call and sends the backend's back, so
	// the results are found by the ids the script gave the calls.
	backend := map[string]string{}
	for _, m := range requests[1].Messages {
		for _, c := range m.ToolCalls {
			backend[c.ID] = c.Wire()
		}
	}
	results := map[string]provider.Message{}
	for _, m := range requests[1].Messages {
		if m.Role == provider.RoleTool {
			results[backend[m.ToolCallID]] = m
		}
	}
	if r := results["c1"]; r.IsError || !strings.Contains(r.Content, "Tokio") {
		t.Errorf("web_search result = %+v, want the search results", r)
	}
	for _, id := range []string{"c2", "c3"} {
		if r := results[id]; !r.IsError || !strings.Contains(r.Content, "unknown tool") {
			t.Errorf("%s result = %+v, want an unknown tool", id, r)
		}
	}
	if len(a.fetched.Requests()) != 0 {
		t.Error("web_fetch reached the network although the chat turned it off")
	}
}
