package server

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

func TestEntryWirePreservesTextAndChannel(t *testing.T) {
	for _, kind := range []store.EntryKind{store.KindPreamble, store.KindNotice} {
		for _, text := range []string{"", "  exact <text>\nnext line & café"} {
			e, err := session.TextEntry(kind, text)
			if err != nil {
				t.Fatal(err)
			}
			e.Channel = "web"
			body, err := asEntry(e)
			if err != nil || body.Channel != "web" || body.Text == nil || *body.Text != text || body.Message.Role != "" {
				t.Fatalf("wire: %+v %v", body, err)
			}
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded["text"] != text || decoded["channel"] != "web" {
				t.Fatalf("JSON: %s", data)
			}
		}
	}
	e, err := session.MessageEntry(provider.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	e.Channel = "web"
	body, err := asEntry(e)
	if err != nil || body.Text != nil || body.Channel != "web" || body.Message.Content != "hello" {
		t.Fatalf("message: %+v %v", body, err)
	}
}

func TestSubagentWaitingReasonWire(t *testing.T) {
	for _, reason := range []string{"", "test wait"} {
		s := &Server{}
		body, err := s.asSession(store.Session{ID: "child", Agent: "unknown", State: store.SessionWaiting, WaitingReason: reason}, configBase{})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		got, present := fields["waiting_reason"]
		if reason == "" && present || reason != "" && got != reason {
			t.Fatalf("wire reason: %s", data)
		}
	}
}
