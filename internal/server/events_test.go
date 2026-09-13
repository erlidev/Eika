package server_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/server"
)

// stream opens an event stream against an in-process server and returns the
// connection, closed when the test ends.
func stream(t *testing.T, s *server.Server, query string) *websocket.Conn {
	t.Helper()
	httpServer := httptest.NewServer(s.Handler())
	t.Cleanup(httpServer.Close)

	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/events?token=" + testToken
	if query != "" {
		url += "&" + query
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial event stream: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// send writes one request to the event stream.
func send(t *testing.T, conn *websocket.Conn, req any) {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write request: %v", err)
	}
}

// next reads the next event from the stream.
func next(t *testing.T, conn *websocket.Conn) event.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	var e event.Event
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("decode event %q: %v", data, err)
	}
	return e
}

// emitUntilSeen publishes an event repeatedly until the client sees one of
// its topic, then returns it. A subscription taken over the socket is in
// place by the time the server has read the request, which the test cannot
// observe directly.
func emitUntilSeen(t *testing.T, s *server.Server, conn *websocket.Conn, typ, topic string, payload any) event.Event {
	t.Helper()
	e, err := event.New(typ, topic, payload)
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			s.Bus().Emit(context.Background(), e)
			select {
			case <-done:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	for {
		got := next(t, conn)
		if got.Type == typ && got.Topic == topic {
			return got
		}
	}
}

func TestEventStreamDeliversSubscribedTopicsOnly(t *testing.T) {
	s := server.New(testConfig(), testLogger(), server.Deps{}, server.Options{})
	conn := stream(t, s, "")

	send(t, conn, map[string]any{"type": "subscribe", "topics": []string{event.SessionTopic("s1")}})
	got := emitUntilSeen(t, s, conn, event.TypeMessageDelta, event.SessionTopic("s1"),
		event.MessageDelta{RunID: "run-1", Text: "hello"})

	var payload event.MessageDelta
	if err := got.DecodePayload(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Text != "hello" {
		t.Errorf("text = %q, want hello", payload.Text)
	}

	// An event on a topic nobody subscribed to never arrives; the next event
	// the client sees is the one on its own topic.
	other, err := event.New(event.TypeTurnEnd, event.SessionTopic("s2"), event.TurnEnd{RunID: "run-2"})
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	s.Bus().Emit(context.Background(), other)
	next := emitUntilSeen(t, s, conn, event.TypeTurnStart, event.SessionTopic("s1"), event.TurnStart{RunID: "run-3"})
	if next.Topic != event.SessionTopic("s1") {
		t.Errorf("topic = %q, want session:s1", next.Topic)
	}
}

func TestEventStreamSubscribesFromTheQuery(t *testing.T) {
	s := server.New(testConfig(), testLogger(), server.Deps{}, server.Options{})
	conn := stream(t, s, "topics="+event.TopicGlobal+","+event.WorkspaceTopic("w1"))

	got := emitUntilSeen(t, s, conn, event.TypeWorkspaceState, event.WorkspaceTopic("w1"),
		event.WorkspaceState{WorkspaceID: "w1", State: "running"})
	if got.Type != event.TypeWorkspaceState {
		t.Errorf("type = %q, want workspace.state", got.Type)
	}
}

func TestEventStreamRejectsAnUnknownRequest(t *testing.T) {
	s := server.New(testConfig(), testLogger(), server.Deps{}, server.Options{})
	conn := stream(t, s, "")

	send(t, conn, map[string]any{"type": "nonsense"})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("the connection stayed open after an unknown request")
	} else if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Errorf("close status = %v, want policy violation", websocket.CloseStatus(err))
	}
}

func TestEventStreamNeedsTheToken(t *testing.T) {
	s := server.New(testConfig(), testLogger(), server.Deps{}, server.Options{})
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/events"
	if conn, _, err := websocket.Dial(ctx, url, nil); err == nil {
		_ = conn.CloseNow()
		t.Fatal("the event stream accepted a connection without a token")
	}
}
