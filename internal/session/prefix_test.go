//go:build docker

package session_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/session"
)

// A server's prompt cache holds the bytes of the conversation a request sent,
// and the next run reads the conversation back from the database. A stored
// conversation must therefore be sent as the very bytes it was sent as before
// it was stored, under both protocols, with and without replayed reasoning.
// The agent loop's own prefix invariants (steering, retries, compaction) are
// the agent package's to test; this is the part the session store owns.
func TestAStoredConversationIsSentAsTheBytesItWasSentAs(t *testing.T) {
	ask := provider.AssistantMessageWithReasoning("Checking both.", "Two checks: a < b && c > d.", []provider.ToolCall{
		{ID: "call_0", Name: "echo", Arguments: provider.ToolArguments(`{"text": "a", "n": 1.50}`)},
		{ID: "call_0", Name: "echo", Arguments: provider.ToolArguments(`{"n":2,"text":"b <&>"}`)},
		{ID: "call_1", Name: "echo", Arguments: provider.ToolArguments(`"value"`)},
		{ID: "call_2", Name: "read", Arguments: provider.ToolArguments(`{"path":`)},
	})
	ask.ReasoningItems = []json.RawMessage{
		json.RawMessage(`{"id":"rs_1","summary":[],"type":"reasoning","content":[{"type":"reasoning_text","text":"a <b> & \"q\"\nline é ☕  "}],"encrypted_content":"","status":"completed"}`),
		json.RawMessage(`{"summary":[],"type":"reasoning","status":null,"encrypted_content":"gAAAA+/==","id":"rs_2","n":1.50}`),
	}
	first := provider.UserMessage("Look at the picture, and check a and b.")
	first.Images = []provider.Image{{MediaType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nnot really\x00\xff"), Width: 16, Height: 16}}
	conversation := []provider.Message{
		first,
		ask,
		provider.ToolResultMessage("call_0", "echo <a> & café \"quoted\"", false),
		provider.ToolResultMessage("call_0", "echo b", false),
		provider.ToolResultMessage("call_1", "value", false),
		provider.ToolResultMessage("call_2", "malformed arguments", true),
		provider.AssistantMessage("Summary done.", nil),
		provider.UserMessage("Go on."),
	}

	for _, api := range []string{"chat", "responses"} {
		for _, preserve := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/preserve=%v", api, preserve), func(t *testing.T) {
				tree, sess := newTree(t)
				st := session.NewStore(tree)
				st.SetChannel("web")
				for _, m := range conversation {
					if err := st.Append(t.Context(), sess.ID, "", m); err != nil {
						t.Fatalf("append %s: %v", m.Role, err)
					}
				}
				loaded, err := st.Load(t.Context(), sess.ID)
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				// User and assistant messages carry the channel; tool results do not.
				if path, err := tree.Path(t.Context(), sess.ID); err != nil || path[0].Channel != "web" || path[1].Channel != "web" || path[2].Channel != "" {
					t.Errorf("channels of the stored entries = %+v, %v", path, err)
				}
				before := requestBody(t, api, preserve, conversation)
				after := requestBody(t, api, preserve, loaded.Conversation.Messages())
				if !bytes.Equal(before, after) {
					t.Errorf("the stored conversation is sent as other bytes:\n  was %s\n  now %s", before, after)
				}
			})
		}
	}
}

// requestBody returns the body the provider of api sends for messages.
func requestBody(t *testing.T, api string, preserve bool, messages []provider.Message) []byte {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		http.Error(w, `{"error":{"message":"recorded"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	build := openai.New
	if api == "responses" {
		build = openai.NewResponses
	}
	p, err := build(provider.Endpoint{BaseURL: srv.URL + "/v1", APIKey: "k"})
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	events, err := p.Stream(t.Context(), provider.Request{Model: "qwen", System: "You are Eika.", Messages: messages, PreserveThinking: preserve})
	if err == nil {
		for range events {
		}
	}
	if body == nil {
		t.Fatal("the provider sent no request")
	}
	return body
}
