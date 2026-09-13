package event_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/event"
)

// busLogger returns a logger that discards everything.
func busLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// emit publishes one event of the given type on a topic.
func emit(t *testing.T, b *event.Bus, typ, topic string, payload any) {
	t.Helper()
	e, err := event.New(typ, topic, payload)
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	b.Emit(context.Background(), e)
}

// receive takes the next event from a subscription, failing if none arrives.
func receive(t *testing.T, s *event.Subscription) event.Event {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		if !ok {
			t.Fatal("subscription closed while waiting for an event")
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
		return event.Event{}
	}
}

func TestBusFansOutByTopic(t *testing.T) {
	b := event.NewBus(busLogger())
	one := b.Subscribe(event.SessionTopic("s1"))
	defer one.Close()
	two := b.Subscribe(event.SessionTopic("s1"), event.TopicGlobal)
	defer two.Close()
	other := b.Subscribe(event.SessionTopic("s2"))
	defer other.Close()

	if b.Subscribers() != 3 {
		t.Fatalf("subscribers = %d, want 3", b.Subscribers())
	}
	emit(t, b, event.TypeTurnStart, event.SessionTopic("s1"), event.TurnStart{RunID: "r1"})
	emit(t, b, event.TypeWorkspaceState, event.TopicGlobal, event.WorkspaceState{WorkspaceID: "w1", State: "running"})

	if got := receive(t, one); got.Type != event.TypeTurnStart {
		t.Errorf("first subscriber got %q, want turn.start", got.Type)
	}
	if len(one.Events()) != 0 {
		t.Errorf("first subscriber queued %d further events, want 0", len(one.Events()))
	}
	if got := receive(t, two); got.Type != event.TypeTurnStart {
		t.Errorf("second subscriber got %q, want turn.start", got.Type)
	}
	if got := receive(t, two); got.Type != event.TypeWorkspaceState {
		t.Errorf("second subscriber got %q, want workspace.state", got.Type)
	}
	if len(other.Events()) != 0 {
		t.Errorf("subscriber of another topic got %d events, want 0", len(other.Events()))
	}
}

func TestBusResubscribeReplacesTopics(t *testing.T) {
	b := event.NewBus(busLogger())
	s := b.Subscribe(event.SessionTopic("s1"))
	defer s.Close()

	s.Subscribe(event.SessionTopic("s2"))
	emit(t, b, event.TypeTurnStart, event.SessionTopic("s1"), event.TurnStart{RunID: "r1"})
	emit(t, b, event.TypeTurnEnd, event.SessionTopic("s2"), event.TurnEnd{RunID: "r2"})

	got := receive(t, s)
	if got.Type != event.TypeTurnEnd {
		t.Errorf("type = %q, want turn.end: the old topic is still delivered", got.Type)
	}
}

func TestBusDropsForASlowSubscriberAndReportsIt(t *testing.T) {
	b := event.NewBus(busLogger())
	s := b.Subscribe(event.SessionTopic("s1"))
	defer s.Close()

	// More events than the subscriber's buffer holds, with nothing reading.
	const sent = 400
	for i := 0; i < sent; i++ {
		emit(t, b, event.TypeMessageDelta, event.SessionTopic("s1"), event.MessageDelta{RunID: "r1"})
	}

	// Drain what fits, then the next event brings the drop report.
	received := 0
	for len(s.Events()) > 0 {
		<-s.Events()
		received++
	}
	if received == 0 || received >= sent {
		t.Fatalf("buffered %d of %d events, want a bounded number", received, sent)
	}
	emit(t, b, event.TypeMessageDelta, event.SessionTopic("s1"), event.MessageDelta{RunID: "r1"})

	got := receive(t, s)
	if got.Type != event.TypeBusDropped {
		t.Fatalf("type = %q, want bus.dropped", got.Type)
	}
	var payload event.BusDropped
	if err := got.DecodePayload(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Dropped != sent-received {
		t.Errorf("dropped = %d, want %d", payload.Dropped, sent-received)
	}
	if next := receive(t, s); next.Type != event.TypeMessageDelta {
		t.Errorf("event after the report = %q, want message.delta", next.Type)
	}
}

func TestBusCloseStopsDelivery(t *testing.T) {
	b := event.NewBus(busLogger())
	s := b.Subscribe(event.TopicGlobal)
	s.Close()
	s.Close() // closing twice is harmless

	emit(t, b, event.TypeTurnStart, event.TopicGlobal, event.TurnStart{RunID: "r1"})
	if _, ok := <-s.Events(); ok {
		t.Error("a closed subscription still delivered an event")
	}
	if b.Subscribers() != 0 {
		t.Errorf("subscribers = %d, want 0", b.Subscribers())
	}
}
