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
		"spaces":            `{"text": "a", "n": 1.50}`,
		"key order":         `{"z":1,"a":2}`,
		"a shell command":   `{"command":"make && go test ./... > out.txt 2>&1 <in"}`,
		"escapes":           `{"text":"café \"quoted\" \/ \n"}`,
		"numbers":           `{"n":1e5,"m":1.0,"big":12345678901234567890}`,
		"nested whitespace": "{\n  \"a\": [1, 2,\t3],\n  \"b\": {\"c\": null}\n}",
		"already compact":   `{"path":"/tmp"}`,
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

func TestExactTextIsStoredOnlyWhereTheValueWouldChange(t *testing.T) {
	// A reader of the entry sees the arguments as a value, as before, and a
	// message whose text encoding keeps has nothing extra to store.
	compact := provider.AssistantMessage("", []provider.ToolCall{{ID: "c", Name: "echo", Arguments: `{"path":"/tmp"}`}})
	raw, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "arguments_text") {
		t.Errorf("compact arguments are stored twice: %s", raw)
	}
	spaced := provider.AssistantMessage("", []provider.ToolCall{{ID: "c", Name: "echo", Arguments: `{"path": "/tmp"}`}})
	if raw, err = json.Marshal(spaced); err != nil {
		t.Fatal(err)
	}
	var shape struct {
		ToolCalls []struct {
			Arguments map[string]string `json:"arguments"`
			Text      string            `json:"arguments_text"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("read %s: %v", raw, err)
	}
	if got := shape.ToolCalls[0]; got.Arguments["path"] != "/tmp" || got.Text != `{"path": "/tmp"}` {
		t.Errorf("stored call = %+v, want the arguments as an object and their exact text", got)
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
