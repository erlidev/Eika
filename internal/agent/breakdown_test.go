package agent_test

import (
	"context"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// conversation is a request with a system prompt in two sections, two tools,
// and a tool round with replayed reasoning.
func conversation() agent.Context {
	call := provider.ToolCall{ID: "c1", Name: "ls", Arguments: provider.ToolArguments(`{"path":"/home/eika"}`)}
	return agent.Context{
		Sections: []agent.Section{
			{Kind: agent.SectionBase, Text: "You are a careful assistant that keeps answers short."},
			{Kind: agent.SectionInstructions, Text: "Always answer in one line."},
		},
		Tools: []agent.ToolSchema{
			{ToolDef: provider.ToolDef{Name: "ls", Description: "list a directory", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
			{ToolDef: provider.ToolDef{Name: "read", Description: "read a file", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
		},
		Messages: []provider.Message{
			provider.UserMessage("what is in my home directory?"),
			provider.AssistantMessageWithReasoning("", "I should list it", []provider.ToolCall{call}),
			provider.ToolResultMessage("c1", "notes.md  todo.txt  projects", false),
			provider.AssistantMessageWithReasoning("Three entries.", "", nil),
		},
		Parameters: agent.Parameters{Model: "fake", PreserveThinking: true, ImageInput: true},
	}
}

func TestEveryPartIsCountedAndNothingIsScaledToFitTheWhole(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := conversation()

	b := agent.MeasureParts(context.Background(), counter.New(server), c)

	if len(b.Sections) != 2 || len(b.Tools) != 2 || len(b.Messages) != 4 {
		t.Fatalf("breakdown has %d sections, %d tools, %d messages; want 2, 2, 4", len(b.Sections), len(b.Tools), len(b.Messages))
	}
	if b.Sections[0].Kind != agent.SectionBase || b.Sections[1].Kind != agent.SectionInstructions {
		t.Errorf("section kinds = %v, %v", b.Sections[0].Kind, b.Sections[1].Kind)
	}
	for i, s := range c.Sections {
		if want := providertest.Tokens(s.Text); b.Sections[i].Tokens != want || b.Sections[i].Approximate {
			t.Errorf("section %s = %+v, want exactly %d", s.Kind, b.Sections[i], want)
		}
	}
	if want := providertest.Tokens("what is in my home directory?"); b.Messages[0].Tokens != want || b.Messages[0].Approximate {
		t.Errorf("user message = %+v, want exactly its text's %d", b.Messages[0], want)
	}
	if want := providertest.Tokens("ls") + providertest.Tokens(`{"path":"/home/eika"}`); b.Messages[1].Tokens != want {
		t.Errorf("tool call message = %d, want its call's name and arguments, %d", b.Messages[1].Tokens, want)
	}
	if want := providertest.Tokens("I should list it"); b.Messages[1].Reasoning != want {
		t.Errorf("reasoning = %d, want %d, counted apart from the message", b.Messages[1].Reasoning, want)
	}
	if b.Messages[2].Reasoning != 0 || b.Messages[3].Reasoning != 0 {
		t.Errorf("messages with no reasoning report %d and %d reasoning tokens", b.Messages[2].Reasoning, b.Messages[3].Reasoning)
	}

	total := 0
	for _, s := range b.Sections {
		total += s.Tokens
	}
	for _, tl := range b.Tools {
		total += tl.Tokens
	}
	for _, m := range b.Messages {
		total += m.Tokens + m.Reasoning
	}
	if b.Parts != total {
		t.Errorf("Parts = %d, the parts add up to %d", b.Parts, total)
	}

	// What the parts leave unexplained is the template: the requests's own
	// count less the parts. It is not spread over them, and here it is the
	// fake template's wrappers, a few tokens a message.
	measured := server.PromptTokens(c.Request())
	remainder := measured - b.Parts
	if remainder <= 0 || remainder > 40 {
		t.Errorf("remainder = %d (measured %d, parts %d), want the small positive cost of the template", remainder, measured, b.Parts)
	}
}

func TestToolsAreSizedByRenderingWhereTheEndpointCan(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := conversation()
	b := agent.MeasureParts(context.Background(), counter.New(server), c)

	with := server.PromptTokens(c.Request())
	bare := c
	bare.Tools = nil
	without := server.PromptTokens(bare.Request())
	got := 0
	for _, tl := range b.Tools {
		if tl.Approximate {
			t.Errorf("tool %s is approximate on an endpoint that renders requests", tl.Name)
		}
		got += tl.Tokens
	}
	if got != with-without {
		t.Errorf("tools add up to %d, want the %d the request grows by with them", got, with-without)
	}
}

func TestPartsAreMarkedApproximateWhereTheEndpointCannotCountThem(t *testing.T) {
	tests := []struct {
		name         string
		routes       providertest.Routes
		textExact    bool
		toolsExact   bool
		approximates bool
	}{
		{"a text-only endpoint tokenises text but cannot render the tools", providertest.RoutesText, true, false, true},
		{"an endpoint that tokenises nothing estimates everything", providertest.RoutesNone, false, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := conversation()
			b := agent.MeasureParts(context.Background(), counter.New(providertest.NewTokenServer(tc.routes)), c)
			for _, s := range b.Sections {
				if s.Approximate == tc.textExact {
					t.Errorf("section %s approximate = %v, want %v", s.Kind, s.Approximate, !tc.textExact)
				}
			}
			for _, tl := range b.Tools {
				if tl.Approximate == tc.toolsExact {
					t.Errorf("tool %s approximate = %v, want %v", tl.Name, tl.Approximate, !tc.toolsExact)
				}
			}
			if b.Approximate() != tc.approximates {
				t.Errorf("Approximate() = %v, want %v", b.Approximate(), tc.approximates)
			}
			if b.Parts == 0 {
				t.Error("an approximate breakdown has no tokens at all")
			}
		})
	}
}

func TestAnExactBreakdownSaysSo(t *testing.T) {
	b := agent.MeasureParts(context.Background(), counter.New(providertest.NewTokenServer(providertest.RoutesPrompt)), conversation())
	if b.Approximate() {
		t.Errorf("breakdown on an endpoint that renders requests is approximate: %+v", b)
	}
}

func TestAPictureIsApproximateUntilItsSizeWasMeasured(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	ctr := counter.New(server)
	c := conversation()
	c.Messages[0].Images = []provider.Image{{MediaType: "image/png", Width: 100, Height: 100, Data: []byte("x")}}

	before := agent.MeasureParts(context.Background(), ctr, c)
	if want := providertest.Tokens("what is in my home directory?") + counter.EstimatedImageTokens(100, 100); before.Messages[0].Tokens != want || !before.Messages[0].Approximate {
		t.Errorf("user message = %+v, want its text and the pixel estimate of its picture, approximate", before.Messages[0])
	}

	// The loop predicts a request and observes its measurement, which is how
	// the counter learns what a picture of this size costs.
	req := c.Request()
	if _, err := ctr.Predict(context.Background(), req); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	ctr.Observe(context.Background(), req, provider.Usage{InputTokens: provider.Reported(server.PromptTokens(req))})

	b := agent.MeasureParts(context.Background(), ctr, c)
	if want := providertest.Tokens("what is in my home directory?") + providertest.ImageTokens(100, 100); b.Messages[0].Tokens != want || b.Messages[0].Approximate {
		t.Errorf("user message = %+v, want %d and exact once the picture's cost was measured", b.Messages[0], want)
	}
}
