package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/provider"
)

// Provider streams responses from an OpenAI-compatible Chat Completions
// endpoint.
type Provider struct {
	client openai.Client
	model  config.Model
}

// New returns a Provider for one configured model. The API key is read from
// the environment variable the model names, which is the only place a key
// exists; a missing variable is an error. It returns the interface type
// because it is the constructor the provider registry holds.
func New(m config.Model) (provider.Provider, error) {
	key, ok := os.LookupEnv(m.APIKeyEnv)
	if ok {
		key = strings.TrimSpace(key)
	}
	if !ok || key == "" {
		return nil, fmt.Errorf("build openai provider for model %s: environment variable %s is empty", m.Name, m.APIKeyEnv)
	}
	opts := []option.RequestOption{
		option.WithAPIKey(key),
		// Retries are the agent loop's job, so that every provider and the
		// scripted fake behave the same way.
		option.WithMaxRetries(0),
	}
	if m.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(m.BaseURL))
	}
	return &Provider{client: openai.NewClient(opts...), model: m}, nil
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
func relay(ctx context.Context, stream chunkStream, out chan<- provider.Event) {
	acc := newToolCalls()
	var usage provider.Usage
	var stop string
	send := func(e provider.Event) bool {
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
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
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				stop = choice.FinishReason
			}
			if choice.Delta.Content != "" && !send(provider.TextDelta(choice.Delta.Content)) {
				return
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
	}
	if err := stream.Err(); err != nil {
		send(provider.Errorf(streamError(err)))
		return
	}
	// A response cut off by the token limit leaves the last tool call's
	// arguments half-written, so report the stop instead of running it.
	if stop == "length" {
		if usage != (provider.Usage{}) && !send(provider.Event{Kind: provider.KindUsage, Usage: usage}) {
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
	if usage != (provider.Usage{}) && !send(provider.Event{Kind: provider.KindUsage, Usage: usage}) {
		return
	}
	send(provider.Done(stop))
}

// params builds the SDK request for one provider request.
func (p *Provider) params(req provider.Request) (openai.ChatCompletionNewParams, error) {
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, openai.SystemMessage(req.System))
	}
	for _, m := range req.Messages {
		converted, err := message(m)
		if err != nil {
			return openai.ChatCompletionNewParams{}, err
		}
		messages = append(messages, converted)
	}

	model := req.Model
	if model == "" {
		model = p.model.Name
	}
	params := openai.ChatCompletionNewParams{
		Model:         shared.ChatModel(model),
		Messages:      messages,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: param.NewOpt(true)},
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.model.MaxOutput
	}
	if maxTokens > 0 {
		params.MaxCompletionTokens = param.NewOpt(int64(maxTokens))
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.ReasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.ReasoningEffort)
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
func message(m provider.Message) (openai.ChatCompletionMessageParamUnion, error) {
	switch m.Role {
	case provider.RoleUser:
		return openai.UserMessage(m.Content), nil
	case provider.RoleTool:
		return openai.ToolMessage(m.Content, m.ToolCallID), nil
	case provider.RoleAssistant:
		msg := openai.ChatCompletionAssistantMessageParam{}
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

// streamError classifies an SDK failure so the agent loop knows whether to
// retry it. Anything that is not an API status error is a transport failure
// and worth one more attempt.
func streamError(err error) error {
	out := &provider.Error{Op: "stream chat completion", Err: err, Retryable: true}
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

var _ provider.Provider = (*Provider)(nil)
