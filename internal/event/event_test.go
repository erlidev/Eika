package event_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/event"
)

// delta is a stand-in for a payload struct owned by an emitting package.
type delta struct {
	Text string `json:"text"`
}

func TestNewStampsUTCAndEncodesPayload(t *testing.T) {
	before := time.Now().UTC()
	e, err := event.New(event.TypeMessageDelta, event.SessionTopic("s1"), delta{Text: "hi"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if e.Type != "message.delta" || e.Topic != "session:s1" {
		t.Errorf("got type %q topic %q", e.Type, e.Topic)
	}
	if e.Time.Location() != time.UTC {
		t.Errorf("time location = %v, want UTC", e.Time.Location())
	}
	if e.Time.Before(before) {
		t.Errorf("time = %v, want at or after %v", e.Time, before)
	}
	var got delta
	if err := e.DecodePayload(&got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got.Text != "hi" {
		t.Errorf("payload text = %q, want hi", got.Text)
	}
}

func TestNewWithoutPayload(t *testing.T) {
	e, err := event.New(event.TypeTurnEnd, event.TopicGlobal, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if len(e.Payload) != 0 {
		t.Errorf("payload = %s, want none", e.Payload)
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "payload") {
		t.Errorf("encoded = %s, want no payload field", data)
	}
}

func TestNewRejectsUnencodablePayload(t *testing.T) {
	if _, err := event.New(event.TypeRunError, event.TopicGlobal, make(chan int)); err == nil {
		t.Fatal("New accepted an unencodable payload")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		topic   string
		payload any
	}{
		{"turn start", event.TypeTurnStart, event.SessionTopic("s1"), nil},
		{"message delta", event.TypeMessageDelta, event.SessionTopic("s1"), delta{Text: "partial"}},
		{"tool call", event.TypeToolCall, event.SessionTopic("s1"), map[string]string{"name": "bash"}},
		{"tool output", event.TypeToolOutput, event.SessionTopic("s1"), delta{Text: "line"}},
		{"tool result", event.TypeToolResult, event.SessionTopic("s1"), delta{Text: "done"}},
		{"turn end", event.TypeTurnEnd, event.SessionTopic("s1"), nil},
		{"run error", event.TypeRunError, event.SessionTopic("s1"), delta{Text: "boom"}},
		{"question asked", event.TypeQuestionAsked, event.SessionTopic("s1"), delta{Text: "which?"}},
		{"subagent started", event.TypeSubagentStarted, event.TopicGlobal, delta{Text: "child"}},
		{"subagent finished", event.TypeSubagentFinished, event.TopicGlobal, delta{Text: "child"}},
		{"workspace state", event.TypeWorkspaceState, event.WorkspaceTopic("w1"), delta{Text: "running"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, err := event.New(c.typ, c.topic, c.payload)
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			data, err := json.Marshal(want)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got event.Event
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !got.Time.Equal(want.Time) {
				t.Errorf("time = %v, want %v", got.Time, want.Time)
			}
			got.Time, want.Time = time.Time{}, time.Time{}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestDecodePayloadErrors(t *testing.T) {
	t.Run("no payload", func(t *testing.T) {
		var v delta
		if err := (event.Event{Type: event.TypeTurnEnd}).DecodePayload(&v); err == nil {
			t.Fatal("DecodePayload accepted an empty payload")
		}
	})
	t.Run("wrong shape", func(t *testing.T) {
		e, err := event.New(event.TypeMessageDelta, event.TopicGlobal, delta{Text: "hi"})
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		var v []string
		if err := e.DecodePayload(&v); err == nil {
			t.Fatal("DecodePayload accepted a mismatched target")
		}
	})
}
