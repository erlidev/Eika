package openai_test

import (
	"encoding/json"
	"net/http"
	"strings"
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
	sent := string(<-s.body)

	encoder, ok := p.(provider.ChatEncoder)
	if !ok {
		t.Fatal("the openai provider does not implement provider.ChatEncoder")
	}
	raw, err := encoder.ChatBody(req)
	if err != nil {
		t.Fatalf("ChatBody: %v", err)
	}
	// The body is the one Stream sends, byte for byte, with the stream flag
	// Stream adds at its end.
	if want := strings.TrimSuffix(string(raw), "}") + `,"stream":true}`; sent != want {
		t.Errorf("ChatBody differs from what Stream sends:\n got  %s\n want %s", sent, want)
	}
}

func TestChatBodyGivesAReasoningOnlyAssistantMessageEmptyContent(t *testing.T) {
	// llama.cpp refuses an assistant message with neither content nor tool
	// calls (400), which is what a response cut off inside its reasoning
	// leaves; every request after it would fail the same way.
	encoder := newProvider(t, "http://model.invalid").(provider.ChatEncoder)
	raw, err := encoder.ChatBody(provider.Request{
		Model: "m", PreserveThinking: true,
		Messages: []provider.Message{
			provider.UserMessage("hi"),
			provider.AssistantMessageWithReasoning("", "thinking", nil),
			provider.AssistantMessageWithReasoning("", "thinking", []provider.ToolCall{{ID: "c1", Name: "ls", Arguments: `{}`}}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if content, present := body.Messages[1]["content"]; !present || content != "" {
		t.Errorf("reasoning-only message = %v, want content \"\"", body.Messages[1])
	}
	if _, present := body.Messages[2]["content"]; present {
		t.Errorf("tool-call-only message = %v, want no content as before", body.Messages[2])
	}
}
