package session_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider/providertest"
)

// This file is the fake model server of the prefix-stability test. It speaks
// llama.cpp's routes: /v1/chat/completions and /v1/responses answer from a
// script, /apply-template renders a chat body with a template of its own, and
// /tokenize counts. It keeps the raw body of every request, which is what the
// test compares: the bytes on the wire, not the values the providers were
// given.

// stepKind says what a scripted response is for, which the test needs to
// know the relation a request has to the ones before it.
type stepKind string

const (
	// stepTurn is an ordinary model call: the loop accepts its response.
	stepTurn stepKind = "turn"
	// stepFail is an attempt that fails with a retryable error, or stalls; the
	// loop sends the same request again.
	stepFail stepKind = "fail"
	// stepReject is a call the server refuses and the loop does not retry.
	stepReject stepKind = "reject"
	// stepSummary is the request a compaction asks its summary with.
	stepSummary stepKind = "summary"
)

// wireCall is a tool call as the backend names it.
type wireCall struct {
	id, name, args string
}

// step is one scripted response.
type step struct {
	kind      stepKind
	reasoning string
	text      string
	calls     []wireCall
	// cutOff ends the response at the output limit.
	cutOff bool
	// status, when it is not zero, answers with that HTTP status and no stream.
	status int
	// stall sends the headers and then nothing, until the client gives up.
	stall bool
}

// modelCall is one request the server received on a model route.
type modelCall struct {
	path string
	body []byte
	step step
	// counted is the chat body the token counter last had rendered before this
	// request arrived: the prompt the loop expected the call to read.
	counted []byte
}

// fakeServer is the fake model server.
type fakeServer struct {
	*httptest.Server

	mu      sync.Mutex
	steps   []step
	next    int
	calls   []modelCall
	counted []byte
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	s := &fakeServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/apply-template":
			s.mu.Lock()
			s.counted = raw
			s.mu.Unlock()
			rendered, _ := renderChat(raw)
			writeJSON(w, map[string]any{"prompt": rendered})
		case "/tokenize":
			var req struct {
				Content      string `json:"content"`
				ParseSpecial bool   `json:"parse_special"`
			}
			_ = json.Unmarshal(raw, &req)
			n := providertest.Tokens(req.Content)
			if !req.ParseSpecial {
				n = 0
				for _, w := range strings.Fields(req.Content) {
					n += (len(w) + 2) / 3
				}
			}
			writeJSON(w, map[string]any{"tokens": make([]int, n)})
		case "/v1/chat/completions", "/v1/responses":
			s.modelCall(t, w, r, raw)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// script appends responses to the ones the server will give.
func (s *fakeServer) script(steps ...step) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, steps...)
}

// recorded returns the model requests received so far, oldest first.
func (s *fakeServer) recorded() []modelCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]modelCall(nil), s.calls...)
}

// drained reports whether every scripted response was used.
func (s *fakeServer) drained() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next == len(s.steps)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// modelCall answers one request on a model route with the next scripted step.
func (s *fakeServer) modelCall(t *testing.T, w http.ResponseWriter, r *http.Request, raw []byte) {
	s.mu.Lock()
	if s.next >= len(s.steps) {
		s.mu.Unlock()
		t.Errorf("the fake server received request %d with no response scripted for it", s.next+1)
		http.Error(w, `{"error":{"message":"no response scripted"}}`, http.StatusInternalServerError)
		return
	}
	st := s.steps[s.next]
	s.next++
	s.calls = append(s.calls, modelCall{path: r.URL.Path, body: raw, step: st, counted: s.counted})
	s.mu.Unlock()

	rendered, _ := promptOf(r.URL.Path, raw)
	prompt := providertest.Tokens(rendered)
	flusher := w.(http.Flusher)
	switch {
	case st.status != 0:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st.status)
		_, _ = io.WriteString(w, `{"error":{"message":"the server said no","type":"scripted"}}`)
		return
	case st.stall:
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	emit := func(event, data string) {
		if event != "" {
			_, _ = io.WriteString(w, "event: "+event+"\n")
		}
		_, _ = io.WriteString(w, "data: "+data+"\n\n")
		flusher.Flush()
	}
	completion := providertest.Tokens(st.reasoning + " " + st.text)
	if r.URL.Path == "/v1/responses" {
		streamResponses(emit, st, prompt, completion)
		return
	}
	streamChat(emit, st, prompt, completion)
}

// promptOf renders the body of a model request as the server reads it.
func promptOf(path string, raw []byte) (prompt, generation string) {
	if path == "/v1/responses" {
		raw = responsesAsChat(raw)
	}
	return renderChat(raw)
}

// mustMarshal encodes v as a server written in C++ does, leaving <, > and & as
// they are; encoding/json would write them as < and the like.
func mustMarshal(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// streamChat writes a Chat Completions stream, as llama.cpp sends one:
// reasoning in reasoning_content, calls whole in the delta that opens them,
// then the finish, then a chunk that carries only the usage.
func streamChat(emit func(event, data string), st step, prompt, completion int) {
	chunk := func(delta map[string]any, finish any) string {
		return mustMarshal(map[string]any{
			"id": "chatcmpl-1", "object": "chat.completion.chunk", "model": "qwen",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		})
	}
	emit("", chunk(map[string]any{"role": "assistant", "content": nil}, nil))
	if st.reasoning != "" {
		emit("", chunk(map[string]any{"reasoning_content": st.reasoning}, nil))
	}
	if st.text != "" {
		emit("", chunk(map[string]any{"content": st.text}, nil))
	}
	for i, c := range st.calls {
		emit("", chunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": c.id, "type": "function",
			"function": map[string]any{"name": c.name, "arguments": c.args},
		}}}, nil))
	}
	finish := "stop"
	switch {
	case st.cutOff:
		finish = "length"
	case len(st.calls) > 0:
		finish = "tool_calls"
	}
	emit("", chunk(map[string]any{}, finish))
	emit("", mustMarshal(map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion.chunk", "model": "qwen", "choices": []any{},
		"usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion},
	}))
	emit("", "[DONE]")
}

// streamResponses writes a Responses stream in the shape llama.cpp records
// (internal/provider/testdata/responses/llamacpp): a reasoning item whose keys
// are in that server's order, a message, function calls, then the completion.
func streamResponses(emit func(event, data string), st step, prompt, completion int) {
	send := func(typ string, fields map[string]any) {
		fields["type"] = typ
		emit(typ, mustMarshal(fields))
	}
	index := 0
	if st.reasoning != "" {
		id := fmt.Sprintf("rs_%x", sha256.Sum256([]byte(st.reasoning)))[:12]
		send("response.output_item.added", map[string]any{"output_index": index, "item": json.RawMessage(
			`{"id":"` + id + `","summary":[],"type":"reasoning","content":[],"encrypted_content":"","status":"in_progress"}`)})
		send("response.reasoning_text.delta", map[string]any{"output_index": index, "content_index": 0, "delta": st.reasoning, "item_id": id})
		send("response.output_item.done", map[string]any{"output_index": index, "item": json.RawMessage(
			`{"id":"` + id + `","summary":[],"type":"reasoning","content":[{"type":"reasoning_text","text":` +
				mustMarshal(st.reasoning) + `}],"encrypted_content":"","status":"completed"}`)})
		index++
	}
	if st.text != "" {
		send("response.output_text.delta", map[string]any{"output_index": index, "content_index": 0, "delta": st.text})
		send("response.output_item.done", map[string]any{"output_index": index, "item": map[string]any{
			"type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": st.text, "annotations": []any{}}},
		}})
		index++
	}
	for _, c := range st.calls {
		item := map[string]any{"type": "function_call", "id": "fc_" + c.name, "call_id": c.id, "name": c.name, "arguments": c.args, "status": "completed"}
		send("response.output_item.added", map[string]any{"output_index": index, "item": map[string]any{
			"type": "function_call", "id": "fc_" + c.name, "call_id": c.id, "name": c.name, "arguments": "", "status": "in_progress"}})
		send("response.function_call_arguments.delta", map[string]any{"output_index": index, "delta": c.args})
		send("response.output_item.done", map[string]any{"output_index": index, "item": item})
		index++
	}
	usage := map[string]any{"input_tokens": prompt, "output_tokens": completion, "total_tokens": prompt + completion}
	if st.cutOff {
		send("response.incomplete", map[string]any{"response": map[string]any{
			"status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"}, "usage": usage}})
		return
	}
	send("response.completed", map[string]any{"response": map[string]any{"status": "completed", "usage": usage}})
}

// chatMessage is a message of a Chat Completions body, as the template reads it.
type chatMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	ReasoningContent string          `json:"reasoning_content"`
	ToolCallID       string          `json:"tool_call_id"`
	ToolCalls        []chatToolCall  `json:"tool_calls"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chatBody is the part of a Chat Completions body the template reads.
type chatBody struct {
	Messages         []chatMessage     `json:"messages"`
	Tools            []json.RawMessage `json:"tools"`
	Kwargs           json.RawMessage   `json:"chat_template_kwargs"`
	PreserveThinking bool              `json:"preserve_thinking"`
}

// generationPrompt starts the model's turn; it is not part of the prefix a
// later request extends, because the reply takes its place.
const generationPrompt = "<|start|>assistant\n"

// renderChat is the fake server's chat template applied to a chat body. It
// returns the whole prompt and the generation prompt it ends with. It reads
// everything a real template reads and prints it, so whatever changes the
// prompt changes the string: the roles, texts and image bytes of each message,
// the reasoning of an assistant message when the request preserves thinking,
// the id, name and arguments of each call and the id each result answers, the
// tools, and the template kwargs.
func renderChat(raw []byte) (prompt, generation string) {
	var body chatBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return "<unreadable>", ""
	}
	var b strings.Builder
	turn := func(role, text string) { fmt.Fprintf(&b, "<|start|>%s\n%s<|end|>\n", role, text) }
	// A server parses the tools before it prints them, so their key order is
	// not what the template shows; the test compares that order on the wire.
	var tools []string
	for _, t := range body.Tools {
		var doc any
		_ = json.Unmarshal(t, &doc)
		tools = append(tools, mustMarshal(doc))
	}
	if len(tools) > 0 {
		turn("tools", strings.Join(tools, "\n"))
	}
	for _, m := range body.Messages {
		switch m.Role {
		case "system":
			turn("system", contentText(m.Content))
		case "user":
			turn("user", contentText(m.Content))
		case "tool":
			turn("tool", m.ToolCallID+"\n"+contentText(m.Content))
		case "assistant":
			text := ""
			if body.PreserveThinking && m.ReasoningContent != "" {
				text += "<|think|>" + m.ReasoningContent + "<|endthink|>\n"
			}
			text += contentText(m.Content)
			for _, c := range m.ToolCalls {
				text += fmt.Sprintf("\n<|call|>%s %s %s<|endcall|>", c.ID, c.Function.Name, c.Function.Arguments)
			}
			turn("assistant", text)
		}
	}
	generation = generationPrompt
	if len(body.Kwargs) > 0 {
		generation += "<|kwargs|>" + string(body.Kwargs) + "<|end|>\n"
	}
	return b.String() + generation, generation
}

// contentText prints a message's content: a string, or parts of text and
// images.
func contentText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		switch p.Type {
		case "image_url":
			sum := sha256.Sum256([]byte(p.ImageURL.URL))
			texts = append(texts, "<|image|>"+hex.EncodeToString(sum[:4]))
		default:
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}

// responsesAsChat converts the body of a Responses request into the chat body
// llama.cpp builds from it before it applies the template: instructions become
// the system message, reasoning items the reasoning_content of the assistant
// message they belong to, function calls its tool_calls, and function call
// outputs tool messages.
func responsesAsChat(raw []byte) []byte {
	var in struct {
		Instructions     string            `json:"instructions"`
		Input            []json.RawMessage `json:"input"`
		Tools            []json.RawMessage `json:"tools"`
		Kwargs           json.RawMessage   `json:"chat_template_kwargs"`
		PreserveThinking bool              `json:"preserve_thinking"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil
	}
	out := map[string]any{"preserve_thinking": in.PreserveThinking}
	if len(in.Kwargs) > 0 {
		out["chat_template_kwargs"] = in.Kwargs
	}
	var tools []json.RawMessage
	for _, t := range in.Tools {
		var flat map[string]any
		_ = json.Unmarshal(t, &flat)
		fn := map[string]any{"name": flat["name"], "description": flat["description"], "parameters": flat["parameters"]}
		tools = append(tools, json.RawMessage(mustMarshal(map[string]any{"type": "function", "function": fn})))
	}
	out["tools"] = tools

	var messages []map[string]any
	if in.Instructions != "" {
		messages = append(messages, map[string]any{"role": "system", "content": in.Instructions})
	}
	var assistant map[string]any
	flush := func() {
		if assistant != nil {
			if _, ok := assistant["content"]; !ok {
				assistant["content"] = ""
			}
			messages = append(messages, assistant)
			assistant = nil
		}
	}
	current := func() map[string]any {
		if assistant == nil {
			assistant = map[string]any{"role": "assistant"}
		}
		return assistant
	}
	for _, item := range in.Input {
		var it struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
			Content   []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL string `json:"image_url"`
			} `json:"content"`
		}
		_ = json.Unmarshal(item, &it)
		switch {
		case it.Type == "reasoning":
			text := ""
			for _, c := range it.Content {
				text += c.Text
			}
			current()["reasoning_content"] = text
		case it.Type == "function_call":
			calls, _ := current()["tool_calls"].([]any)
			current()["tool_calls"] = append(calls, map[string]any{
				"id": it.CallID, "type": "function",
				"function": map[string]any{"name": it.Name, "arguments": it.Arguments},
			})
		case it.Type == "function_call_output":
			flush()
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": it.CallID, "content": it.Output})
		case it.Type == "message" && it.Role == "assistant":
			text := ""
			for _, c := range it.Content {
				text += c.Text
			}
			current()["content"] = text
		case it.Type == "message":
			flush()
			var parts []any
			for _, c := range it.Content {
				if c.Type == "input_image" {
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": c.ImageURL}})
				} else {
					parts = append(parts, map[string]any{"type": "text", "text": c.Text})
				}
			}
			messages = append(messages, map[string]any{"role": it.Role, "content": parts})
		}
	}
	flush()
	out["messages"] = messages
	return []byte(mustMarshal(out))
}
