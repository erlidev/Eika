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

// chatWire is a session as a chat reports it: no workspace, and the tools its
// next run offers.
type chatWire struct {
	ID              string   `json:"id"`
	WorkspaceID     *string  `json:"workspace_id"`
	Title           string   `json:"title"`
	Kind            string   `json:"kind"`
	ParentSessionID string   `json:"parent_session_id"`
	Tools           []string `json:"tools"`
}

type chatsWire struct {
	Sessions []chatWire `json:"sessions"`
}

type toolsWire struct {
	Tools []struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		NeedsWorkspace bool   `json:"needs_workspace"`
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
	project, _ := a.newProject(t, "demo")
	ws := a.newWorkspace(t, project.ID)
	work := a.newSession(t, ws.ID)

	chat := a.newChat(t, "a question")
	if chat.WorkspaceID != nil {
		t.Errorf("chat workspace_id = %q, want it absent", *chat.WorkspaceID)
	}
	if !slices.Equal(chat.Tools, standaloneTools) {
		t.Errorf("new chat tools = %v, want every standalone tool %v", chat.Tools, standaloneTools)
	}

	cases := []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"a chat in a workspace", map[string]any{"chat": true, "workspace_id": ws.ID, "title": "x"}, 400},
		{"a chat without a title", map[string]any{"chat": true, "title": " "}, 400},
		// Leaving the workspace out is a missing workspace, never a chat
		// nobody asked for.
		{"neither a chat nor a workspace", map[string]any{"title": "x"}, 404},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refused(t, request(t, a.Server, "POST", "/api/sessions", c.body), c.status)
		})
	}
	refused(t, request(t, a.Server, "GET", "/api/sessions?chats=true&workspace_id="+ws.ID, nil), 400)

	chats := decodeBody[chatsWire](t, request(t, a.Server, "GET", "/api/sessions?chats=true", nil), 200)
	if len(chats.Sessions) != 1 || chats.Sessions[0].ID != chat.ID {
		t.Errorf("chats = %+v, want the one chat", chats.Sessions)
	}
	inWorkspace := decodeBody[chatsWire](t, request(t, a.Server, "GET", "/api/sessions?workspace_id="+ws.ID, nil), 200)
	if len(inWorkspace.Sessions) != 1 || inWorkspace.Sessions[0].ID != work.ID {
		t.Errorf("workspace sessions = %+v, want the workspace's own", inWorkspace.Sessions)
	}
	if len(inWorkspace.Sessions[0].Tools) != 13 {
		t.Errorf("workspace session tools = %v, want all 13", inWorkspace.Sessions[0].Tools)
	}

	tools := decodeBody[toolsWire](t, request(t, a.Server, "GET", "/api/tools", nil), 200)
	var free []string
	for _, tl := range tools.Tools {
		if tl.Description == "" {
			t.Errorf("tool %s has no description", tl.Name)
		}
		if !tl.NeedsWorkspace {
			free = append(free, tl.Name)
		}
	}
	if len(tools.Tools) != 13 || !slices.Equal(free, standaloneTools) {
		t.Errorf("tools = %+v, want 13 with %v standalone", tools.Tools, standaloneTools)
	}
}

func TestSessionToolsAreChosenAndChecked(t *testing.T) {
	a := newAPI(t)
	chat := a.newChat(t, "a question")
	put := func(id string, tools any) *chatWire {
		t.Helper()
		rec := request(t, a.Server, "PUT", "/api/sessions/"+id+"/tools", map[string]any{"tools": tools})
		if rec.Code != 200 {
			return nil
		}
		got := decodeBody[chatWire](t, rec, 200)
		return &got
	}

	cases := []struct {
		name   string
		tools  any
		status int
	}{
		{"a tool that does not exist", []string{"teleport"}, 400},
		{"a tool that needs a workspace", []string{"web_search", "bash"}, 400},
		{"no list at all", nil, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := request(t, a.Server, "PUT", "/api/sessions/"+chat.ID+"/tools", map[string]any{"tools": c.tools})
			refused(t, rec, c.status)
		})
	}
	refused(t, request(t, a.Server, "PUT", "/api/sessions/missing/tools",
		map[string]any{"tools": []string{}}), 404)

	if got := put(chat.ID, []string{"web_search", "ask_user", "web_search"}); got == nil ||
		!slices.Equal(got.Tools, []string{"ask_user", "web_search"}) {
		t.Errorf("chosen tools = %+v, want ask_user and web_search once each", got)
	}
	if got := put(chat.ID, []string{}); got == nil || got.Tools == nil || len(got.Tools) != 0 {
		t.Errorf("no tools = %+v, want an empty list", got)
	}
	read := decodeBody[struct {
		Session chatWire `json:"session"`
	}](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID, nil), 200)
	if len(read.Session.Tools) != 0 {
		t.Errorf("tools read back = %v, want none", read.Session.Tools)
	}

	// A workspace session may narrow its tools too, to any of them.
	work := a.session(t)
	if got := put(work.ID, []string{"read", "bash"}); got == nil || !slices.Equal(got.Tools, []string{"bash", "read"}) {
		t.Errorf("workspace session tools = %+v, want bash and read", got)
	}
}

func TestChatForksStayChats(t *testing.T) {
	a := newAPI(t)
	chat := a.newChat(t, "a question")
	a.script(providertest.Text("an answer"))
	a.postMessage(t, chat.ID, "hello", "", 202)
	a.waitIdle(t, chat.ID)
	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/path", nil), 200)
	last := path.Entries[len(path.Entries)-1].ID

	rec := request(t, a.Server, "PUT", "/api/sessions/"+chat.ID+"/tools", map[string]any{"tools": []string{"web_fetch"}})
	decodeBody[chatWire](t, rec, 200)

	rec = request(t, a.Server, "POST", "/api/sessions/"+chat.ID+"/fork",
		map[string]any{"entry_id": last, "with_workspace": true})
	refused(t, rec, 400)

	fork := decodeBody[chatWire](t, request(t, a.Server, "POST", "/api/sessions/"+chat.ID+"/fork",
		map[string]any{"entry_id": last}), 201)
	if fork.WorkspaceID != nil || fork.Kind != "fork" || fork.ParentSessionID != chat.ID {
		t.Errorf("fork = %+v, want a chat forked from %s", fork, chat.ID)
	}
	if !slices.Equal(fork.Tools, []string{"web_fetch"}) {
		t.Errorf("fork tools = %v, want the chat's choice", fork.Tools)
	}
}

// A chat's run has no workspace and offers the model exactly the tools the
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
	if system := requests[0].System; !strings.Contains(system, "no workspace") {
		t.Errorf("system prompt = %q, want the chat rules", system)
	}

	results := map[string]provider.Message{}
	for _, m := range requests[1].Messages {
		if m.Role == provider.RoleTool {
			results[m.ToolCallID] = m
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
