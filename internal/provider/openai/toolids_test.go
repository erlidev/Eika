package openai_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

// backendCall is a call the loop has minted an id for: id is Eika's, wire what
// the backend called it.
func backendCall(id, wire, name string) provider.ToolCall {
	return provider.ToolCall{ID: id, WireID: &wire, Name: name, Arguments: `{}`}
}

// idConversation is a conversation with every kind of tool call id in it:
// parallel calls the backend gave one id, a call it gave none, and a call
// stored before the loop minted ids, which has the one id.
func idConversation() []provider.Message {
	return []provider.Message{
		provider.UserMessage("go"),
		provider.AssistantMessage("", []provider.ToolCall{
			backendCall("call_aaa", "call_0", "ls"),
			backendCall("call_bbb", "call_0", "ls"),
			backendCall("call_ccc", "", "ls"),
			{ID: "toolu_legacy", Name: "ls", Arguments: `{}`},
		}),
		provider.ToolResultMessage("call_aaa", "a", false),
		provider.ToolResultMessage("call_bbb", "b", false),
		provider.ToolResultMessage("call_ccc", "c", false),
		provider.ToolResultMessage("toolu_legacy", "d", false),
	}
}

// wantWire is the id each call and its result are sent under: the backend's,
// Eika's where the backend gave none, and the only one a legacy call has.
var wantWire = []string{"call_0", "call_0", "call_ccc", "toolu_legacy"}

func TestChatSendsTheBackendsToolCallIDsAndOnlyThose(t *testing.T) {
	encoder := newProvider(t, "http://model.invalid").(provider.ChatEncoder)
	raw, err := encoder.ChatBody(provider.Request{Model: "m", Messages: idConversation()})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []struct {
			ToolCalls []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	var calls, results []string
	for _, c := range body.Messages[1].ToolCalls {
		calls = append(calls, c.ID)
	}
	for _, m := range body.Messages[2:] {
		results = append(results, m.ToolCallID)
	}
	if !reflect.DeepEqual(calls, wantWire) || !reflect.DeepEqual(results, wantWire) {
		t.Errorf("chat sent call ids %v and result ids %v, want %v for both", calls, results, wantWire)
	}
}

func TestResponsesSendsTheBackendsToolCallIDsAndOnlyThose(t *testing.T) {
	s := stream(t, "llamacpp/answer.sse")
	req := bench
	req.Messages = idConversation()
	read(t, newResponses(t, s.URL, false), req)

	var calls, results []string
	for _, item := range s.sent(t)["input"].([]any) {
		item := item.(map[string]any)
		switch item["type"] {
		case "function_call":
			calls = append(calls, item["call_id"].(string))
		case "function_call_output":
			results = append(results, item["call_id"].(string))
		}
	}
	if !reflect.DeepEqual(calls, wantWire) || !reflect.DeepEqual(results, wantWire) {
		t.Errorf("responses sent call ids %v and result ids %v, want %v for both", calls, results, wantWire)
	}
}

func TestAStoredConversationReplaysAsAValidRequestUnderEitherProtocol(t *testing.T) {
	// A session whose endpoint changed protocol is sent its stored messages
	// again. The ids must survive the trip through storage and be non-empty on
	// the wire, which Responses requires and a chat server may.
	stored, err := json.Marshal(idConversation())
	if err != nil {
		t.Fatal(err)
	}
	var replayed []provider.Message
	if err := json.Unmarshal(stored, &replayed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, idConversation()) {
		t.Fatalf("the conversation changed in storage:\n got  %+v\n want %+v", replayed, idConversation())
	}

	chat := newProvider(t, "http://model.invalid").(provider.ChatEncoder)
	responses := newResponses(t, "http://model.invalid", false).(provider.ChatEncoder)
	for name, encoder := range map[string]provider.ChatEncoder{"chat": chat, "responses": responses} {
		before, err := encoder.ChatBody(provider.Request{Model: "m", Messages: idConversation()})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		after, err := encoder.ChatBody(provider.Request{Model: "m", Messages: replayed})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(before) != string(after) {
			t.Errorf("%s sends different bytes for the replayed conversation:\n before %s\n after  %s", name, before, after)
		}
	}
	for id, wire := range provider.WireToolCallIDs(replayed) {
		if wire == "" {
			t.Errorf("call %s would be sent under an empty id", id)
		}
	}
}
