package provider_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

// A request sends the text of a tool call's arguments and of a reasoning item
// as the model produced it, and a later request sends them again from the
// stored message. The prompt cache reads those bytes, so a message must come
// back from its JSON as the bytes it went in as. encoding/json would compact
// a document and write < > & as < > &.
func TestAMessageComesBackFromItsJSONAsTheBytesItWasSentAs(t *testing.T) {
	texts := map[string]string{
		"spaces and number forms": `{"text": "a", "n": 1.50, "big":12345678901234567890}`,
		"key order":               `{"z":1,"a":2}`,
		"a shell command":         `{"command":"make && go test ./... > out.txt 2>&1 <in"}`,
	}
	for name, text := range texts {
		t.Run(name, func(t *testing.T) {
			sent := provider.AssistantMessage("", []provider.ToolCall{{ID: "call_1", Name: "echo", Arguments: provider.ToolArguments(text)}})
			sent.ReasoningItems = []json.RawMessage{json.RawMessage(strings.ReplaceAll(`{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"ARGS"}], "encrypted_content":""}`, "ARGS", `a < b && c > d`))}
			raw, err := json.Marshal(sent)
			if err != nil {
				t.Fatal(err)
			}
			var got provider.Message
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("read %s: %v", raw, err)
			}
			if string(got.ToolCalls[0].Arguments) != text {
				t.Errorf("arguments came back as %q, want %q (stored as %s)", got.ToolCalls[0].Arguments, text, raw)
			}
			if string(got.ReasoningItems[0]) != string(sent.ReasoningItems[0]) {
				t.Errorf("reasoning item came back as %s, want %s (stored as %s)", got.ReasoningItems[0], sent.ReasoningItems[0], raw)
			}
		})
	}
}

func TestAnEntryWrittenBeforeExactTextStillReads(t *testing.T) {
	old := `{"role":"assistant","tool_calls":[{"id":"c","name":"echo","arguments":{"a":1}}],"reasoning_items":[{"type":"reasoning","summary":[]}]}`
	var got provider.Message
	if err := json.Unmarshal([]byte(old), &got); err != nil {
		t.Fatal(err)
	}
	if string(got.ToolCalls[0].Arguments) != `{"a":1}` || string(got.ReasoningItems[0]) != `{"type":"reasoning","summary":[]}` {
		t.Errorf("an old entry read as %+v", got)
	}
}
