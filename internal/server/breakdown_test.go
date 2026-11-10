package server

import (
	"context"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/providertest"
)

func TestTheRemainderIsWhatTheEndpointCountedLessThePartsAndIsNeverClamped(t *testing.T) {
	tests := []struct {
		name          string
		parts         int
		prompt        *int
		wantRemainder *int
	}{
		{"the template costs something", 100, new(112), new(12)},
		{"the parts add up to exactly the prompt", 100, new(100), new(0)},
		{"the parts add up to more than the prompt", 100, new(96), new(-4)},
		{"no prompt to state it against", 100, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newBreakdown(agent.Breakdown{Parts: tc.parts}, tc.prompt, promptMeasured)
			switch {
			case tc.wantRemainder == nil && (b.Remainder != nil || b.PromptTokens != nil || b.PromptSource != ""):
				t.Errorf("breakdown = %+v, want no prompt, source or remainder", b)
			case tc.wantRemainder != nil && (b.Remainder == nil || *b.Remainder != *tc.wantRemainder || b.PromptSource != promptMeasured):
				t.Errorf("remainder = %v, source %q; want %d, measured", b.Remainder, b.PromptSource, *tc.wantRemainder)
			}
		})
	}
}

// next is the request a session would send next, with a system prompt, a tool
// and a message.
func next() agent.Context {
	return agent.Context{
		Sections: []agent.Section{{Kind: agent.SectionBase, Text: "You are a careful assistant."}},
		Tools: []agent.ToolSchema{{ToolDef: provider.ToolDef{
			Name: "ls", Description: "list a directory", Schema: []byte(`{"type":"object"}`),
		}}},
		Messages:   []provider.Message{provider.UserMessage("what is in my home directory?")},
		Parameters: agent.Parameters{Model: "fake"},
	}
}

func TestTheNextRequestStatesItsRemainderAgainstAnExactPrediction(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	ctr := counter.New(server)
	c := next()

	pred := predictedNext(context.Background(), ctr, c)
	if pred.Method != "exact" || pred.Tokens != server.PromptTokens(c.Request()) {
		t.Fatalf("prediction = %+v, want the exact %d", pred, server.PromptTokens(c.Request()))
	}
	b := breakdownOfNext(context.Background(), ctr, c, pred)
	if b.PromptTokens == nil || *b.PromptTokens != pred.Tokens || b.PromptSource != promptPredicted {
		t.Fatalf("breakdown prompt = %v, source %q; want the exact prediction's %d", b.PromptTokens, b.PromptSource, pred.Tokens)
	}
	if b.Remainder == nil || *b.Remainder != pred.Tokens-b.Parts || *b.Remainder <= 0 {
		t.Errorf("remainder = %v, want the prediction %d less the parts' %d, positive", b.Remainder, pred.Tokens, b.Parts)
	}
}

func TestTheNextRequestStatesNoRemainderAgainstAGuess(t *testing.T) {
	for name, routes := range map[string]providertest.Routes{
		"an endpoint that only tokenises text, before it has learned": providertest.RoutesText,
		"an endpoint that tokenises nothing":                          providertest.RoutesNone,
	} {
		t.Run(name, func(t *testing.T) {
			ctr := counter.New(providertest.NewTokenServer(routes))
			c := next()
			pred := predictedNext(context.Background(), ctr, c)
			if pred.Method != "estimated" {
				t.Fatalf("prediction method = %q, want estimated", pred.Method)
			}
			b := breakdownOfNext(context.Background(), ctr, c, pred)
			if b.PromptTokens != nil || b.Remainder != nil || b.PromptSource != "" {
				t.Errorf("breakdown = prompt %v, remainder %v, source %q; want none against an estimate", b.PromptTokens, b.Remainder, b.PromptSource)
			}
			if b.Parts == 0 {
				t.Error("the parts add up to nothing")
			}
		})
	}
}

func TestAPreviewDoesNotStandInForTheNextRunsPrediction(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	ctr := counter.New(server)
	c := next()
	predictedNext(context.Background(), ctr, c)

	usage := provider.Usage{InputTokens: provider.Reported(server.PromptTokens(c.Request()))}
	if _, ok := ctr.Observe(context.Background(), c.Request(), usage); ok {
		t.Error("the counter matched a measurement to a preview, which would hide a run's own drift")
	}
}
