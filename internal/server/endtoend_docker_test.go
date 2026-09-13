//go:build docker

package server_test

import (
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// waitSubscribed waits until the stream handler has taken its subscription,
// which happens after the handshake the client already saw finish.
func (a *api) waitSubscribed(t *testing.T) {
	t.Helper()
	waitFor(t, "the event stream to subscribe", func() bool { return a.Bus().Subscribers() > 0 })
}

// collect reads events until one of type stop arrives, and returns them all.
func collect(t *testing.T, conn *websocket.Conn, stop string) []event.Event {
	t.Helper()
	var got []event.Event
	for {
		e := next(t, conn)
		got = append(got, e)
		if e.Type == stop {
			return got
		}
	}
}

// typesOf lists the types of a run of events, in order.
func typesOf(events []event.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}

// TestEndToEnd walks the whole harness: a local project on a directory, a
// workspace on it, a session in the workspace, one message whose model asks
// for a shell command, the events that reach a client on the WebSocket, and
// the entries the run leaves in the tree.
func TestEndToEnd(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	conn := stream(t, a.Server, "topics="+event.SessionTopic(sess.ID))
	a.waitSubscribed(t)

	a.script(
		providertest.Calls("running it", providertest.Call("c1", "bash", map[string]any{"command": "echo hi"})),
		providertest.Text("it printed hi"),
	)
	run := a.postMessage(t, sess.ID, "run echo hi", "", 202)

	events := collect(t, conn, event.TypeTurnEnd)
	kinds := strings.Join(typesOf(events), " ")
	for _, want := range []string{
		event.TypeTurnStart, event.TypeMessageDelta, event.TypeToolCall,
		event.TypeToolResult, event.TypeTurnEnd,
	} {
		if !strings.Contains(kinds, want) {
			t.Errorf("event types = %q, want a %s among them", kinds, want)
		}
	}
	var result event.ToolResult
	for _, e := range events {
		if e.Type == event.TypeToolResult {
			if err := e.DecodePayload(&result); err != nil {
				t.Fatalf("decode tool.result: %v", err)
			}
		}
	}
	if result.Name != "bash" || result.IsError || !strings.Contains(result.Content, "hi") {
		t.Errorf("tool result = %+v, want the command's output", result)
	}
	for _, e := range events {
		if e.Topic != event.SessionTopic(sess.ID) {
			t.Errorf("event %s arrived on topic %q, want the session's", e.Type, e.Topic)
		}
	}

	state := a.waitIdle(t, sess.ID)
	if state.Run == nil || state.Run.ID != run.ID || state.Run.State != "done" {
		t.Fatalf("run = %+v, want %s done", state.Run, run.ID)
	}

	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	if len(path.Entries) != 4 {
		t.Fatalf("entries = %d, want user, assistant, tool result, assistant: %+v", len(path.Entries), path.Entries)
	}
	last := path.Entries[len(path.Entries)-1]
	if last.Kind != "assistant" || last.Message.Content != "it printed hi" {
		t.Errorf("last entry = %+v, want the final answer", last)
	}
	if last.Commit == "" {
		t.Error("the assistant entry recorded no workspace commit")
	}
}

func TestEventStreamReplaysASessionFromAnEntry(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)

	a.script(providertest.Text("an answer"))
	a.postMessage(t, sess.ID, "a question", "", 202)
	a.waitIdle(t, sess.ID)

	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	if len(path.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(path.Entries))
	}

	t.Run("from the query", func(t *testing.T) {
		conn := stream(t, a.Server, "since="+path.Entries[0].ID)
		e := next(t, conn)
		if e.Type != event.TypeSessionMessage {
			t.Fatalf("type = %q, want session.message", e.Type)
		}
		var payload event.SessionMessage
		if err := e.DecodePayload(&payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if payload.EntryID != path.Entries[1].ID || payload.Kind != "assistant" {
			t.Errorf("payload = %+v, want the entry after the one asked for", payload)
		}
		if payload.SessionID != sess.ID {
			t.Errorf("session_id = %q, want %q", payload.SessionID, sess.ID)
		}
	})

	t.Run("on request", func(t *testing.T) {
		conn := stream(t, a.Server, "")
		send(t, conn, map[string]any{"type": "session.replay", "session_id": sess.ID})
		for i, want := range path.Entries {
			e := next(t, conn)
			var payload event.SessionMessage
			if err := e.DecodePayload(&payload); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if payload.EntryID != want.ID {
				t.Fatalf("entry %d = %q, want %q", i, payload.EntryID, want.ID)
			}
		}
	})

	t.Run("an entry that is not on the branch", func(t *testing.T) {
		conn := stream(t, a.Server, "since=nope")
		if _, _, err := conn.Read(t.Context()); err == nil {
			t.Fatal("the stream stayed open after an unknown entry")
		} else if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Errorf("close status = %v, want policy violation", websocket.CloseStatus(err))
		}
	})
}

func TestEventStreamReportsWorkspaceState(t *testing.T) {
	a := newAPI(t)
	project, _ := a.newProject(t, "demo")
	ws := a.newWorkspace(t, project.ID)

	conn := stream(t, a.Server, "topics="+event.WorkspaceTopic(ws.ID))
	a.waitSubscribed(t)

	if rec := request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/stop", nil); rec.Code != 200 {
		t.Fatalf("stop = %d", rec.Code)
	}
	e := next(t, conn)
	if e.Type != event.TypeWorkspaceState {
		t.Fatalf("type = %q, want workspace.state", e.Type)
	}
	var payload event.WorkspaceState
	if err := e.DecodePayload(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.WorkspaceID != ws.ID || payload.State != "stopped" {
		t.Errorf("payload = %+v, want the workspace stopped", payload)
	}
}
