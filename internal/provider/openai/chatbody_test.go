package openai_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

func TestChatBodyIsTheBodyStreamSends(t *testing.T) {
	s := newSSEServer(t, http.StatusOK,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	p := newProvider(t, s.URL)

	effort, maxOut, topK := provider.EffortNone, 32, 20
	req := provider.Request{
		Model: "m", System: "be brief", ThinkingSwitch: provider.SwitchTemplate, PreserveThinking: true,
		Sampling: provider.Sampling{ReasoningEffort: &effort, MaxOutput: &maxOut, TopK: &topK},
		Tools:    []provider.ToolDef{{Name: "ls", Description: "list", Schema: []byte(`{"type":"object"}`)}},
		Messages: []provider.Message{
			provider.UserMessage("hi"),
			provider.AssistantMessageWithReasoning("", "thinking", []provider.ToolCall{{ID: "c1", Name: "ls", Arguments: `{}`}}),
			provider.ToolResultMessage("c1", "a b", false),
		},
	}
	collect(t, p, req)
	var sent map[string]any
	if err := json.Unmarshal(<-s.body, &sent); err != nil {
		t.Fatalf("decode the request Stream sent: %v", err)
	}

	encoder, ok := p.(provider.ChatEncoder)
	if !ok {
		t.Fatal("the openai provider does not implement provider.ChatEncoder")
	}
	raw, err := encoder.ChatBody(req)
	if err != nil {
		t.Fatalf("ChatBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("ChatBody is not JSON: %v", err)
	}
	if sent["stream"] != true {
		t.Errorf("Stream sent stream = %v, want true", sent["stream"])
	}
	delete(sent, "stream")
	if !reflect.DeepEqual(body, sent) {
		t.Errorf("ChatBody differs from what Stream sends:\n got  %v\n want %v", body, sent)
	}
}

func TestChatBodyRefusesARequestStreamRefuses(t *testing.T) {
	encoder := newProvider(t, "http://model.invalid").(provider.ChatEncoder)
	if _, err := encoder.ChatBody(provider.Request{}); err == nil {
		t.Error("ChatBody of a request that names no model returned no error")
	}
}
