package providertest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/erlidev/eika/internal/provider"
)

// Step is one scripted model response. Events are streamed in order; a Step
// with Err set fails the call instead.
type Step struct {
	Events []provider.Event
	Err    error
}

// Text returns a step that streams text in one delta and stops.
func Text(text string) Step {
	return Step{Events: []provider.Event{
		provider.TextDelta(text),
		provider.Done("stop"),
	}}
}

// Calls returns a step that streams optional text and then the given tool
// calls, as a model does when it wants tools run.
func Calls(text string, calls ...provider.ToolCall) Step {
	events := make([]provider.Event, 0, len(calls)+2)
	if text != "" {
		events = append(events, provider.TextDelta(text))
	}
	for i, c := range calls {
		events = append(events, provider.Event{
			Kind:       provider.KindToolCall,
			Index:      i,
			ToolCallID: c.ID,
			ToolName:   c.Name,
			ToolCall:   c,
		})
	}
	return Step{Events: append(events, provider.Done("tool_calls"))}
}

// Call builds a tool call whose arguments are the JSON encoding of args.
func Call(id, name string, args any) provider.ToolCall {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(fmt.Sprintf("providertest: encode arguments for %s: %v", name, err))
	}
	return provider.ToolCall{ID: id, Name: name, Arguments: provider.ToolArguments(raw)}
}

// Fail returns a step whose call fails with err before any event.
func Fail(err error) Step { return Step{Err: err} }

// Stream returns a step that streams exactly the given events. Use it when a
// test needs a shape the other constructors do not produce.
func Stream(events ...provider.Event) Step { return Step{Events: events} }

// Provider replays scripted steps and records the requests it received.
type Provider struct {
	mu       sync.Mutex
	steps    []Step
	next     int
	requests []provider.Request
}

// New returns a Provider that replays steps in order.
func New(steps ...Step) *Provider { return &Provider{steps: steps} }

// Stream records the request and replays the next scripted step.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	if p.next >= len(p.steps) {
		p.mu.Unlock()
		return nil, fmt.Errorf("providertest: no scripted response for call %d", p.next+1)
	}
	step := p.steps[p.next]
	p.next++
	p.mu.Unlock()

	if step.Err != nil {
		return nil, step.Err
	}
	out := make(chan provider.Event)
	go func() {
		defer close(out)
		for _, e := range step.Events {
			select {
			case out <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Requests returns the requests the provider received, oldest first.
func (p *Provider) Requests() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Request(nil), p.requests...)
}

// Calls reports how many times Stream was called.
func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

var _ provider.Provider = (*Provider)(nil)
