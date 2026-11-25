package event_test

import (
	"encoding/json"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
)

func TestToolCallArgumentsKeepTheirValidityMarker(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments provider.ToolArguments
		malformed bool
	}{
		{"JSON string", provider.ToolArguments(`"value"`), false},
		{"malformed JSON", provider.ToolArguments(`{"path":`), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, err := event.New(event.TypeToolCall, event.SessionTopic("s1"), event.ToolCall{
				RunID: "r1", CallID: "c1", Name: "read", Arguments: test.arguments,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			var wire struct {
				ArgumentsMalformed bool `json:"arguments_malformed"`
			}
			if err := json.Unmarshal(e.Payload, &wire); err != nil {
				t.Fatalf("decode wire payload: %v", err)
			}
			if wire.ArgumentsMalformed != test.malformed {
				t.Errorf("arguments_malformed = %t, want %t", wire.ArgumentsMalformed, test.malformed)
			}
			var got event.ToolCall
			if err := e.DecodePayload(&got); err != nil {
				t.Fatalf("DecodePayload: %v", err)
			}
			if got.Arguments != test.arguments {
				t.Errorf("arguments = %q, want exact text %q", got.Arguments, test.arguments)
			}
		})
	}
}
