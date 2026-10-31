package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/erlidev/eika/internal/provider"
)

// eventSource is the part of the SDK's server-sent event decoder the relay
// uses.
type eventSource interface {
	Next() bool
	Event() ssestream.Event
	Err() error
}

// eventReader reads the events of a streamed Responses reply.
type eventReader struct {
	ssestream.Decoder
	resp *http.Response
}

// newEventReader returns the reader of a successful reply.
func newEventReader(resp *http.Response) *eventReader {
	return &eventReader{Decoder: ssestream.NewDecoder(resp), resp: resp}
}

// Close ends the reply, which releases its connection.
func (r *eventReader) Close() { _ = r.resp.Body.Close() }

// envelope is what every Responses event carries that the relay reads. The
// events are decoded as raw JSON: the items they hold are kept as the
// endpoint sent them.
type envelope struct {
	Type        string          `json:"type"`
	Delta       string          `json:"delta"`
	OutputIndex int             `json:"output_index"`
	Item        json.RawMessage `json:"item"`
	Response    json.RawMessage `json:"response"`
	Code        string          `json:"code"`
	Message     string          `json:"message"`
}

// item is the part of an output item the relay reads.
type item struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// terminal is the part of the response object a finished stream's last event
// carries that the relay reads.
type terminal struct {
	Status            string            `json:"status"`
	Output            []json.RawMessage `json:"output"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// streamedCall is a function call being assembled from its events. The
// completed item, when it arrives, is the authority; the fragments stand in
// for an endpoint that sends no completed item.
type streamedCall struct {
	index int
	id    string
	name  string
	args  strings.Builder
	done  *item
}

// relayResponses converts the events of a Responses stream into provider
// events until it ends. maxOutput is the output limit the request carried, or
// zero.
func relayResponses(ctx context.Context, src eventSource, maxOutput int, out chan<- provider.Event) {
	send := func(e provider.Event) bool {
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}

	var (
		calls        = map[int]*streamedCall{}
		reasoning    []json.RawMessage
		sawText      bool
		answerText   strings.Builder
		summaryParts int
		final        []byte
		finalType    string
	)
	for src.Next() {
		ev := src.Event()
		var e envelope
		if err := json.Unmarshal(ev.Data, &e); err != nil {
			continue
		}
		switch e.Type {
		case "response.output_text.delta", "response.refusal.delta":
			if e.Delta == "" {
				continue
			}
			sawText = true
			if !send(provider.TextDelta(e.Delta)) {
				return
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if e.Delta == "" {
				continue
			}
			if !send(provider.Event{Kind: provider.KindReasoningDelta, ReasoningDelta: e.Delta}) {
				return
			}
		case "response.reasoning_summary_part.added":
			// Parts of a summary are paragraphs.
			summaryParts++
			if summaryParts > 1 && !send(provider.Event{Kind: provider.KindReasoningDelta, ReasoningDelta: "\n\n"}) {
				return
			}
		case "response.output_item.added":
			var it item
			if json.Unmarshal(e.Item, &it) != nil || it.Type != "function_call" {
				continue
			}
			call := callAt(calls, e.OutputIndex)
			call.id, call.name = firstNonEmpty(it.CallID, it.ID), it.Name
			if !send(provider.Event{
				Kind: provider.KindToolCallDelta, Index: call.index,
				ToolCallID: call.id, ToolName: call.name,
			}) {
				return
			}
		case "response.function_call_arguments.delta":
			call := callAt(calls, e.OutputIndex)
			call.args.WriteString(e.Delta)
			if !send(provider.Event{
				Kind: provider.KindToolCallDelta, Index: call.index, ArgumentsDelta: e.Delta,
			}) {
				return
			}
		case "response.output_item.done":
			var it item
			if json.Unmarshal(e.Item, &it) != nil {
				continue
			}
			switch it.Type {
			case "function_call":
				call := callAt(calls, e.OutputIndex)
				call.done = &it
			case "reasoning":
				if json.Valid(e.Item) {
					reasoning = append(reasoning, append(json.RawMessage(nil), e.Item...))
				}
			case "message":
				for _, part := range it.Content {
					answerText.WriteString(part.Text)
				}
			}
		case "response.completed", "response.incomplete":
			final, finalType = ev.Data, e.Type
		case "response.failed":
			var t terminal
			_ = json.Unmarshal(e.Response, &t)
			send(provider.Errorf(responseError(t.Error.Code, t.Error.Message)))
			return
		case "error":
			send(provider.Errorf(responseError(e.Code, e.Message)))
			return
		}
		if final != nil {
			break
		}
	}
	if err := src.Err(); err != nil {
		send(provider.Errorf(apiError("stream response", err)))
		return
	}
	if final == nil {
		send(provider.Errorf(errors.New("stream response: stream ended without a completed response")))
		return
	}

	var e envelope
	_ = json.Unmarshal(final, &e)
	var t terminal
	_ = json.Unmarshal(e.Response, &t)
	usage, _ := provider.ResponsesUsage(final)

	// An endpoint that sent no completed items still states its output in the
	// final response.
	if len(reasoning) == 0 && len(calls) == 0 && answerText.Len() == 0 && !sawText {
		reasoning, calls, answerText = outputOf(t.Output)
	}
	if !sawText && answerText.Len() > 0 {
		if !send(provider.TextDelta(answerText.String())) {
			return
		}
	}
	for _, raw := range reasoning {
		if !send(provider.Event{Kind: provider.KindReasoningItem, ReasoningItem: raw}) {
			return
		}
	}

	stop := stopReason(finalType, t, len(calls) > 0, usage, maxOutput)
	// A response cut off by the token limit leaves the last tool call's
	// arguments half-written, so the stop is reported instead of the call.
	if stop != "length" {
		for i, call := range orderedCalls(calls) {
			if !send(provider.Event{
				Kind: provider.KindToolCall, Index: i,
				ToolCallID: call.ID, ToolName: call.Name, ToolCall: call,
			}) {
				return
			}
		}
	}
	timings, _ := parseTimings(string(final), usage)
	if usage.Reported() || timings.Known() {
		if !send(provider.Event{Kind: provider.KindUsage, Usage: usage, Timings: timings}) {
			return
		}
	}
	send(provider.Done(stop))
}

// callAt returns the call at an output index, creating it in arrival order.
func callAt(calls map[int]*streamedCall, outputIndex int) *streamedCall {
	if c, ok := calls[outputIndex]; ok {
		return c
	}
	c := &streamedCall{index: len(calls)}
	calls[outputIndex] = c
	return c
}

// orderedCalls returns the assembled tool calls in the order the endpoint
// produced them. Arguments that are empty become an empty JSON object, which
// is what a tool with no required parameters expects.
func orderedCalls(calls map[int]*streamedCall) []provider.ToolCall {
	keys := make([]int, 0, len(calls))
	for k := range calls {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]provider.ToolCall, 0, len(keys))
	for _, k := range keys {
		c := calls[k]
		id, name, args := c.id, c.name, c.args.String()
		if c.done != nil {
			id, name, args = firstNonEmpty(c.done.CallID, c.done.ID), c.done.Name, c.done.Arguments
		}
		if args = strings.TrimSpace(args); args == "" {
			args = "{}"
		}
		out = append(out, provider.ToolCall{ID: id, Name: name, Arguments: provider.ToolArguments(args)})
	}
	return out
}

// outputOf reads the items of a final response's output, for an endpoint that
// sent no item of its own as it went.
func outputOf(output []json.RawMessage) (reasoning []json.RawMessage, calls map[int]*streamedCall, text strings.Builder) {
	calls = map[int]*streamedCall{}
	for i, raw := range output {
		var it item
		if json.Unmarshal(raw, &it) != nil {
			continue
		}
		switch it.Type {
		case "reasoning":
			reasoning = append(reasoning, raw)
		case "function_call":
			c := callAt(calls, i)
			c.done = &it
		case "message":
			for _, part := range it.Content {
				text.WriteString(part.Text)
			}
		}
	}
	return reasoning, calls, text
}

// stopReason maps how a response ended onto the stop reasons the loop knows.
// An endpoint reports a response that hit its output limit as incomplete; one
// that does not (llama.cpp reports it complete) is told by its output count
// reaching the limit the request set, which the cut-off answer is kept for and
// no tool call of it is run.
func stopReason(eventType string, t terminal, hasCalls bool, usage provider.Usage, maxOutput int) string {
	if eventType == "response.incomplete" || t.Status == "incomplete" {
		switch t.IncompleteDetails.Reason {
		case "max_output_tokens":
			return "length"
		case "content_filter":
			return "content_filter"
		case "":
			return "length"
		}
		return t.IncompleteDetails.Reason
	}
	if out, ok := usage.OutputTokens.Value(); ok && maxOutput > 0 && out >= maxOutput {
		return "length"
	}
	if hasCalls {
		return "tool_calls"
	}
	return "stop"
}

// responseError turns the failure an event states into a provider error. The
// code is in the text, so that the wording the loop matches an overflow by
// (context_length_exceeded) is there whatever the message says.
func responseError(code, message string) error {
	text := message
	switch {
	case code != "" && message != "":
		text = code + ": " + message
	case code != "":
		text = code
	case message == "":
		text = "the endpoint reported a failure"
	}
	retryable := false
	switch code {
	case "server_error", "rate_limit_exceeded", "overloaded", "overloaded_error", "service_unavailable", "timeout":
		retryable = true
	}
	return &provider.Error{Op: "stream response", Err: errors.New(text), Retryable: retryable}
}

// firstNonEmpty returns the first of its arguments that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
