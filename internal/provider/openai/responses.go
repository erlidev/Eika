package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/openai/openai-go/v3/option"

	"github.com/erlidev/eika/internal/provider"
)

// Responses streams responses from an endpoint's Responses API, used
// statelessly: nothing is stored on the server (store is false) and every
// request carries the whole conversation as input. It is the other
// implementation of provider.Provider on the same endpoints as Provider, so
// the loop, the accounting and the compaction do not see which one is in use.
//
// It wraps the chat Provider of the same endpoint, and borrows from it what
// the two protocols share: the HTTP client, the authentication, the error
// classification, the model list and the chat body (ChatBody) that token
// counting renders.
type Responses struct {
	chat   *Provider
	hosted bool
}

// NewResponses returns a Responses provider on one endpoint, with the same
// rules for the key and the base URL as New. It returns the interface type
// because it is the constructor the provider registry holds.
func NewResponses(e provider.Endpoint) (provider.Provider, error) {
	chat, err := New(e)
	if err != nil {
		return nil, err
	}
	return &Responses{chat: chat.(*Provider), hosted: e.Hosted}, nil
}

// Stream sends one request and converts the endpoint's server-sent events into
// provider events. The returned channel is closed after a done or error event;
// a caller that stops reading must cancel ctx.
func (p *Responses) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	return streamWithSlot(ctx, req, p.chat.slotHints, p.stream)
}

// stream is one attempt at a request, naming its slot when withSlot says so.
func (p *Responses) stream(ctx context.Context, req provider.Request, withSlot bool) (<-chan provider.Event, error) {
	var slot *int
	if withSlot {
		slot = req.SlotHint
	}
	body, err := p.body(req, true, slot)
	if err != nil {
		return nil, err
	}
	var resp *http.Response
	err = p.chat.client.Post(ctx, "responses", body, &resp,
		option.WithHeader("Accept", "text/event-stream"))
	if err != nil {
		return nil, apiError("stream response", err)
	}
	events := newEventReader(resp)
	maxOutput := 0
	if req.Sampling.MaxOutput != nil {
		maxOutput = *req.Sampling.MaxOutput
	}
	out := make(chan provider.Event)
	go func() {
		defer close(out)
		defer events.Close()
		relayResponses(ctx, events, maxOutput, out)
	}()
	return out, nil
}

// ChatBody returns the Chat Completions body equivalent to req, which a
// server's rendering route takes to count it. Both protocols send the one
// serialisation, so what is counted is what the model reads provided the
// server converts Responses input to chat as the chat request says
// (docs/design/runtime.md section 1).
func (p *Responses) ChatBody(req provider.Request) ([]byte, error) {
	return p.chat.ChatBody(req)
}

// Models lists the models the endpoint serves.
func (p *Responses) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	return p.chat.Models(ctx)
}

// body builds the request. Keys are sorted, so a request is byte-stable, and
// no character is escaped that JSON does not require, so the text a server
// reads is the text the conversation holds. It names slot when it is not nil.
func (p *Responses) body(req provider.Request, stream bool, slot *int) ([]byte, error) {
	if req.Model == "" {
		return nil, errors.New("build response: request names no model")
	}
	input, err := p.input(req)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{
		"model":  req.Model,
		"input":  input,
		"stream": stream,
		"store":  false,
	}
	if req.System != "" {
		fields["instructions"] = req.System
	}
	if tools, err := p.tools(req.Tools); err != nil {
		return nil, err
	} else if len(tools) > 0 {
		fields["tools"] = tools
	}
	p.sampling(req, fields)
	if slot != nil {
		fields[slotField] = *slot
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fields); err != nil {
		return nil, fmt.Errorf("encode response request: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// sampling sets the fields that steer generation. A hosted API takes the ones
// the Responses specification defines and refuses any other, so those are the
// only ones it is sent; a server on the user's hardware reads the rest beside
// them, the way it reads them beside the fields of Chat Completions.
func (p *Responses) sampling(req provider.Request, fields map[string]any) {
	s := req.Sampling
	if s.MaxOutput != nil {
		fields["max_output_tokens"] = *s.MaxOutput
	}
	if s.Temperature != nil {
		fields["temperature"] = *s.Temperature
	}
	if s.TopP != nil {
		fields["top_p"] = *s.TopP
	}
	effort := ""
	if s.ReasoningEffort != nil {
		effort = *s.ReasoningEffort
	}
	switch {
	case effort == "":
	case !p.hosted && effort == provider.EffortNone && req.ThinkingSwitch == provider.SwitchTemplate:
		// Both names, because templates disagree on the one they read and
		// ignore the other.
		fields["chat_template_kwargs"] = map[string]any{"enable_thinking": false, "thinking": false}
	case !p.hosted && effort == provider.EffortNone && req.ThinkingSwitch == provider.SwitchThinking:
		fields["thinking"] = map[string]any{"type": "disabled"}
	default:
		fields["reasoning"] = map[string]any{"effort": effort}
	}
	if p.hosted {
		// store is false, so the server returns a reasoning item only as
		// encrypted content, and only for a model that reasons. It is asked
		// for when the reasoning will be sent back; otherwise nothing would
		// read it.
		if req.PreserveThinking {
			fields["include"] = []string{"reasoning.encrypted_content"}
		}
		return
	}
	if s.TopK != nil {
		fields["top_k"] = *s.TopK
	}
	if s.MinP != nil {
		fields["min_p"] = *s.MinP
	}
	if s.FrequencyPenalty != nil {
		fields["frequency_penalty"] = *s.FrequencyPenalty
	}
	if s.PresencePenalty != nil {
		fields["presence_penalty"] = *s.PresencePenalty
	}
	if s.Seed != nil {
		fields["seed"] = *s.Seed
	}
	if len(s.Stop) > 0 {
		fields["stop"] = s.Stop
	}
	if req.PreserveThinking {
		fields["preserve_thinking"] = true
	}
}

// tools converts the tool definitions into function tools. Strictness is
// stated because the Responses API defaults to strict, which refuses a schema
// that is not closed, where Chat Completions does not.
func (p *Responses) tools(defs []provider.ToolDef) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(defs))
	for _, t := range defs {
		parameters := json.RawMessage(`{"type":"object","properties":{}}`)
		if len(t.Schema) > 0 {
			if !json.Valid(t.Schema) {
				return nil, fmt.Errorf("encode schema of tool %s: not valid JSON", t.Name)
			}
			parameters = t.Schema
		}
		tool := map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  parameters,
		}
		if p.hosted {
			tool["strict"] = false
		}
		out = append(out, tool)
	}
	return out, nil
}

// input converts the conversation into input items.
func (p *Responses) input(req provider.Request) ([]any, error) {
	items := make([]any, 0, len(req.Messages)+1)
	for _, m := range req.Messages {
		converted, err := p.items(m, req.PreserveThinking)
		if err != nil {
			return nil, err
		}
		items = append(items, converted...)
	}
	return items, nil
}

// items converts one message into the input items that stand for it. An
// assistant message becomes its reasoning, its answer and its tool calls, in
// that order.
func (p *Responses) items(m provider.Message, preserveThinking bool) ([]any, error) {
	switch m.Role {
	case provider.RoleUser:
		return []any{userItem(m)}, nil
	case provider.RoleTool:
		return []any{map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content}}, nil
	case provider.RoleAssistant:
		var items []any
		if preserveThinking && (m.Content != "" || len(m.ToolCalls) > 0 || !p.hosted) {
			for _, raw := range p.reasoningItems(m) {
				items = append(items, raw)
			}
		}
		if m.Content != "" {
			items = append(items, map[string]any{
				"type": "message", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": m.Content, "annotations": []any{}}},
			})
		}
		for _, c := range m.ToolCalls {
			items = append(items, map[string]any{
				"type": "function_call", "call_id": c.ID, "name": c.Name, "arguments": string(c.Arguments),
			})
		}
		return items, nil
	default:
		return nil, fmt.Errorf("convert message: unknown role %q", m.Role)
	}
}

// reasoningItems returns the reasoning items to send for an assistant
// message: the ones the endpoint returned, as they were returned. A message
// from a Chat Completions endpoint has only its reasoning text; a hosted API
// accepts nothing it did not return, so none is sent, and a server on the
// user's hardware is sent an item holding the text, the shape it returns
// itself, so the conversation reads the same after the protocol changed.
func (p *Responses) reasoningItems(m provider.Message) []json.RawMessage {
	if len(m.ReasoningItems) > 0 {
		return m.ReasoningItems
	}
	if m.Reasoning == "" || p.hosted {
		return nil
	}
	item, err := json.Marshal(map[string]any{
		"type":    "reasoning",
		"summary": []any{},
		"content": []any{map[string]any{"type": "reasoning_text", "text": m.Reasoning}},
	})
	if err != nil {
		return nil
	}
	return []json.RawMessage{item}
}

// userItem renders a user message as an input message: the images first, each
// as a data URL, then the text, as the chat serialisation does.
func userItem(m provider.Message) map[string]any {
	parts := make([]any, 0, len(m.Images)+1)
	for _, img := range m.Images {
		parts = append(parts, map[string]any{
			"type": "input_image", "detail": "auto",
			"image_url": "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
		})
	}
	if m.Content != "" || len(parts) == 0 {
		parts = append(parts, map[string]any{"type": "input_text", "text": m.Content})
	}
	return map[string]any{"type": "message", "role": "user", "content": parts}
}

var (
	_ provider.Provider    = (*Responses)(nil)
	_ provider.Lister      = (*Responses)(nil)
	_ provider.ChatEncoder = (*Responses)(nil)
)
