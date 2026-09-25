package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"github.com/erlidev/eika/internal/provider"
)

// Provider streams responses from an OpenAI-compatible Chat Completions
// endpoint and lists the models it serves.
type Provider struct {
	client openai.Client
}

// New returns a Provider on one endpoint. An empty key is sent as it is,
// because a local endpoint may ask for none; the harness environment is never
// consulted for one. It returns the interface type because it is the
// constructor the provider registry holds.
func New(e provider.Endpoint) (provider.Provider, error) {
	if e.BaseURL == "" {
		return nil, errors.New("build openai provider: base url is empty")
	}
	opts := []option.RequestOption{
		// The key and the base URL come from the user's provider settings.
		// Set explicitly, even when empty, they override the OPENAI_API_KEY
		// and OPENAI_BASE_URL the SDK would read from the environment.
		option.WithAPIKey(e.APIKey),
		option.WithBaseURL(e.BaseURL),
		// Retries are the agent loop's job, so that every provider and the
		// scripted fake behave the same way.
		option.WithMaxRetries(0),
	}
	return &Provider{client: openai.NewClient(opts...)}, nil
}

// Stream sends one request and converts the SDK's chunks into provider events.
// The returned channel is closed after a done or error event; a caller that
// stops reading must cancel ctx.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	params, err := p.params(req)
	if err != nil {
		return nil, err
	}
	stream := p.client.Chat.Completions.NewStreaming(ctx, params)
	out := make(chan provider.Event)
	go func() {
		defer close(out)
		defer stream.Close()
		relay(ctx, stream, out)
	}()
	return out, nil
}

// chunkStream is the part of the SDK stream this package uses.
type chunkStream interface {
	Next() bool
	Current() openai.ChatCompletionChunk
	Err() error
}

// relay converts SDK chunks into provider events until the stream ends.
// Usage is forwarded as soon as a chunk carries it, so an endpoint that
// reports it per chunk keeps the caller's context view current while the
// response is still arriving; an endpoint that reports it once still yields
// exactly one usage event. An endpoint that measures its own speed reports it
// in the chunk that carries the usage, so the two travel together.
func relay(ctx context.Context, stream chunkStream, out chan<- provider.Event) {
	acc := newToolCalls()
	var usage, reported provider.Usage
	var timings, reportedTimings provider.Timings
	var stop string
	send := func(e provider.Event) bool {
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	sendUsage := func() bool {
		// Timings without usage still say something true, and an endpoint
		// that hangs them off a chunk of its own would otherwise lose them.
		if usage == (provider.Usage{}) && !timings.Known() {
			return true
		}
		if usage == reported && timings == reportedTimings {
			return true
		}
		reported, reportedTimings = usage, timings
		return send(provider.Event{Kind: provider.KindUsage, Usage: usage, Timings: timings})
	}

	for stream.Next() {
		chunk := stream.Current()
		if u := chunk.Usage; u.TotalTokens != 0 || u.PromptTokens != 0 || u.CompletionTokens != 0 {
			usage = provider.Usage{
				InputTokens:  int(u.PromptTokens),
				OutputTokens: int(u.CompletionTokens),
				TotalTokens:  int(u.TotalTokens),
			}
		}
		if t, ok := parseTimings(chunk.RawJSON(), usage); ok {
			timings = t
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				stop = choice.FinishReason
			}
			if choice.Delta.Content != "" && !send(provider.TextDelta(choice.Delta.Content)) {
				return
			}
			// Reasoning is relayed whatever the preserve_thinking setting is:
			// showing the model thinking is the client's business, and
			// replaying it to the endpoint is the request's.
			if reasoning := reasoningContent(choice.Delta); reasoning != "" {
				if !send(provider.Event{
					Kind:           provider.KindReasoningDelta,
					ReasoningDelta: reasoning,
				}) {
					return
				}
			}
			for _, call := range choice.Delta.ToolCalls {
				acc.add(call)
				if !send(provider.Event{
					Kind:           provider.KindToolCallDelta,
					Index:          int(call.Index),
					ToolCallID:     call.ID,
					ToolName:       call.Function.Name,
					ArgumentsDelta: call.Function.Arguments,
				}) {
					return
				}
			}
		}
		if !sendUsage() {
			return
		}
	}
	if err := stream.Err(); err != nil {
		send(provider.Errorf(streamError(err)))
		return
	}
	if stop == "" {
		send(provider.Errorf(errors.New("stream chat completion: stream ended without finish_reason")))
		return
	}
	// A response cut off by the token limit leaves the last tool call's
	// arguments half-written, so report the stop instead of running it.
	if stop == "length" {
		if !sendUsage() {
			return
		}
		send(provider.Done(stop))
		return
	}
	for i, call := range acc.calls() {
		if !send(provider.Event{
			Kind:       provider.KindToolCall,
			Index:      i,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			ToolCall:   call,
		}) {
			return
		}
	}
	if !sendUsage() {
		return
	}
	send(provider.Done(stop))
}

// reasoningContent reads the reasoning_content extension used by compatible
// Chat Completions endpoints. The SDK keeps unknown response fields in the
// raw JSON, so this does not need a provider-specific SDK type.
func reasoningContent(delta openai.ChatCompletionChunkChoiceDelta) string {
	var fields struct {
		ReasoningContent string `json:"reasoning_content"`
	}
	if err := json.Unmarshal([]byte(delta.RawJSON()), &fields); err != nil {
		return ""
	}
	return fields.ReasoningContent
}

// params builds the SDK request for one provider request.
func (p *Provider) params(req provider.Request) (openai.ChatCompletionNewParams, error) {
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, openai.SystemMessage(req.System))
	}
	for _, m := range req.Messages {
		converted, err := message(m, req.PreserveThinking)
		if err != nil {
			return openai.ChatCompletionNewParams{}, err
		}
		messages = append(messages, converted)
	}

	if req.Model == "" {
		return openai.ChatCompletionNewParams{}, errors.New("build chat completion: request names no model")
	}
	params := openai.ChatCompletionNewParams{
		Model:         shared.ChatModel(req.Model),
		Messages:      messages,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: param.NewOpt(true)},
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = param.NewOpt(int64(req.MaxTokens))
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	extra := map[string]any{}
	switch {
	case req.ReasoningEffort == "":
	case req.ReasoningEffort == provider.EffortNone && req.ThinkingSwitch == provider.SwitchTemplate:
		// Both names, because templates disagree on the one they read and
		// ignore the other.
		extra["chat_template_kwargs"] = map[string]any{"enable_thinking": false, "thinking": false}
	case req.ReasoningEffort == provider.EffortNone && req.ThinkingSwitch == provider.SwitchThinking:
		extra["thinking"] = map[string]any{"type": "disabled"}
	default:
		params.ReasoningEffort = shared.ReasoningEffort(req.ReasoningEffort)
	}
	if req.PreserveThinking {
		extra["preserve_thinking"] = true
	}
	if len(extra) > 0 {
		params.SetExtraFields(extra)
	}
	for _, t := range req.Tools {
		var schema shared.FunctionParameters
		if len(t.Schema) > 0 {
			if err := json.Unmarshal(t.Schema, &schema); err != nil {
				return openai.ChatCompletionNewParams{}, fmt.Errorf("encode schema of tool %s: %w", t.Name, err)
			}
		}
		params.Tools = append(params.Tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: param.NewOpt(t.Description),
			Parameters:  schema,
		}))
	}
	return params, nil
}

// message converts one conversation message into its SDK form.
func message(m provider.Message, preserveThinking bool) (openai.ChatCompletionMessageParamUnion, error) {
	switch m.Role {
	case provider.RoleUser:
		return openai.UserMessage(m.Content), nil
	case provider.RoleTool:
		return openai.ToolMessage(m.Content, m.ToolCallID), nil
	case provider.RoleAssistant:
		msg := openai.ChatCompletionAssistantMessageParam{}
		if preserveThinking && m.Reasoning != "" {
			msg.SetExtraFields(map[string]any{"reasoning_content": m.Reasoning})
		}
		if m.Content != "" {
			msg.Content.OfString = param.NewOpt(m.Content)
		}
		for _, c := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: c.ID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name:      c.Name,
						Arguments: string(c.Arguments),
					},
				},
			})
		}
		return openai.ChatCompletionMessageParamUnion{OfAssistant: &msg}, nil
	default:
		return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("convert message: unknown role %q", m.Role)
	}
}

// Models lists the models the endpoint serves, sorted by id. Compatible
// endpoints add fields of their own to each model; the ones that say how
// large its context is are read when present, so the setup screens can
// suggest limits instead of guessing them.
func (p *Provider) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	pages := p.client.Models.ListAutoPaging(ctx)
	var out []provider.ModelInfo
	for pages.Next() {
		m := pages.Current()
		info := provider.ModelInfo{ID: m.ID}
		info.ContextWindow, info.MaxOutput = modelLimits(m.RawJSON())
		out = append(out, info)
	}
	if err := pages.Err(); err != nil {
		return nil, apiError("list models", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// modelLimits reads the context window and the output limit that compatible
// endpoints report beside a model's id. OpenRouter says context_length and
// top_provider.max_completion_tokens, vLLM max_model_len, and Groq
// context_window and max_completion_tokens. Zero means nobody said.
func modelLimits(raw string) (contextWindow, maxOutput int) {
	var fields struct {
		ContextLength       int `json:"context_length"`
		ContextWindow       int `json:"context_window"`
		MaxModelLen         int `json:"max_model_len"`
		MaxCompletionTokens int `json:"max_completion_tokens"`
		TopProvider         struct {
			ContextLength       int `json:"context_length"`
			MaxCompletionTokens int `json:"max_completion_tokens"`
		} `json:"top_provider"`
	}
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return 0, 0
	}
	contextWindow = firstPositive(fields.ContextLength, fields.ContextWindow, fields.MaxModelLen, fields.TopProvider.ContextLength)
	maxOutput = firstPositive(fields.TopProvider.MaxCompletionTokens, fields.MaxCompletionTokens)
	return contextWindow, maxOutput
}

// firstPositive returns the first value above zero, or zero.
func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// streamError classifies an SDK failure so the agent loop knows whether to
// retry it. Anything that is not an API status error is a transport failure
// and worth one more attempt.
func streamError(err error) error {
	return apiError("stream chat completion", err)
}

// apiError classifies an SDK failure of the operation op. Anything that is
// not an API status error is a transport failure and worth one more attempt.
func apiError(op string, err error) error {
	out := &provider.Error{Op: op, Err: err, Retryable: true}
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return out
	}
	out.StatusCode = apiErr.StatusCode
	out.Retryable = apiErr.StatusCode == http.StatusRequestTimeout ||
		apiErr.StatusCode == http.StatusConflict ||
		apiErr.StatusCode == http.StatusTooManyRequests ||
		apiErr.StatusCode >= http.StatusInternalServerError
	if apiErr.Response != nil {
		out.RetryAfter = retryAfter(apiErr.Response.Header)
	}
	return out
}

// retryAfter reads the Retry-After header in either of its forms: a number of
// seconds, or an HTTP date.
func retryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	when, err := http.ParseTime(v)
	if err != nil {
		return 0
	}
	if d := time.Until(when); d > 0 {
		return d
	}
	return 0
}

var (
	_ provider.Provider = (*Provider)(nil)
	_ provider.Lister   = (*Provider)(nil)
)
