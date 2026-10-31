package counter_test

import (
	"context"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/providertest"
)

var toolList = []provider.ToolDef{
	{Name: "ls", Description: "list a directory", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
	{Name: "read", Description: "read a file and return what it says", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"},"lines":{"type":"integer"}}}`)},
	{Name: "bash", Description: "run a command", Schema: []byte(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
}

func sum(ns []int) int {
	total := 0
	for _, n := range ns {
		total += n
	}
	return total
}

func TestToolSizesAddUpToWhatTheTemplateMakesOfTheWholeList(t *testing.T) {
	for name, routes := range map[string]providertest.Routes{
		"an endpoint that shows its prompt": providertest.RoutesPrompt,
		"an endpoint that counts requests":  providertest.RoutesRequest,
	} {
		t.Run(name, func(t *testing.T) {
			server := providertest.NewTokenServer(routes)
			c := counter.New(server)
			req := provider.Request{Model: "fake", System: "be brief", Tools: toolList,
				Messages: []provider.Message{provider.UserMessage("hello there")}}

			sizes, exact := c.ToolSizes(context.Background(), req)
			if !exact || len(sizes) != len(toolList) {
				t.Fatalf("ToolSizes = %v, exact %v; want a rendered size for each of %d tools", sizes, exact, len(toolList))
			}
			// The same request rendered with and without its tools differs by
			// the tools' sizes, whatever else it holds.
			without := req
			without.Tools = nil
			want := server.PromptTokens(req) - server.PromptTokens(without)
			if got := sum(sizes); got != want {
				t.Errorf("tool sizes %v add up to %d, want the %d the template adds for the list", sizes, got, want)
			}
			for i, n := range sizes {
				if n <= 0 {
					t.Errorf("tool %s has size %d, want a positive size", toolList[i].Name, n)
				}
			}
		})
	}
}

func TestToolSizesAreRememberedAcrossCalls(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := counter.New(server)
	req := provider.Request{Model: "fake", Tools: toolList}
	first, _ := c.ToolSizes(context.Background(), req)
	renders := server.RenderCalls()
	if renders == 0 {
		t.Fatal("the first count asked the endpoint to render nothing")
	}
	// A conversation that grew since changes nothing about the tools.
	req.Messages = []provider.Message{provider.UserMessage("a different conversation")}
	again, _ := c.ToolSizes(context.Background(), req)
	if server.RenderCalls() != renders {
		t.Errorf("rendering requests went from %d to %d, want the tool list's sizes remembered", renders, server.RenderCalls())
	}
	for i := range first {
		if first[i] != again[i] {
			t.Errorf("tool %d: %d then %d", i, first[i], again[i])
		}
	}
}

func TestToolSizesWithoutRenderingAreTheDefinitionsAsText(t *testing.T) {
	tests := []struct {
		name   string
		routes providertest.Routes
	}{
		{"an endpoint that only tokenises text", providertest.RoutesText},
		{"an endpoint that tokenises nothing", providertest.RoutesNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := counter.New(providertest.NewTokenServer(tc.routes))
			sizes, exact := c.ToolSizes(context.Background(), provider.Request{Model: "fake", Tools: toolList})
			if exact {
				t.Error("sizes of tool definitions as text were called the endpoint's own")
			}
			if len(sizes) != len(toolList) || sum(sizes) == 0 {
				t.Errorf("sizes = %v, want a size for each tool", sizes)
			}
		})
	}
	t.Run("a text endpoint counts the JSON exactly as text", func(t *testing.T) {
		c := counter.New(providertest.NewTokenServer(providertest.RoutesText))
		sizes, _ := c.ToolSizes(context.Background(), provider.Request{Tools: toolList[:1]})
		raw := `{"name":"ls","description":"list a directory","schema":{"type":"object","properties":{"path":{"type":"string"}}}}`
		if want := providertest.Tokens(raw); sizes[0] != want {
			t.Errorf("size = %d, want the text's %d", sizes[0], want)
		}
	})
}

func TestToolSizesOfNoToolsAreNone(t *testing.T) {
	c := counter.New(providertest.NewTokenServer(providertest.RoutesPrompt))
	if sizes, exact := c.ToolSizes(context.Background(), provider.Request{}); len(sizes) != 0 || !exact {
		t.Errorf("ToolSizes of no tools = %v, %v; want none", sizes, exact)
	}
}

func TestSizeIsExactWhereTheEndpointTokenisesText(t *testing.T) {
	text := "remember the milk and call the bank on monday"
	for name, tc := range map[string]struct {
		routes providertest.Routes
		exact  bool
	}{
		"llama.cpp style": {providertest.RoutesPrompt, true},
		"vLLM style":      {providertest.RoutesRequest, true},
		"text only":       {providertest.RoutesText, true},
		"nothing":         {providertest.RoutesNone, false},
	} {
		t.Run(name, func(t *testing.T) {
			c := counter.New(providertest.NewTokenServer(tc.routes))
			n, exact := c.Size(context.Background(), text)
			if exact != tc.exact {
				t.Errorf("exact = %v, want %v", exact, tc.exact)
			}
			if tc.exact && n != providertest.Tokens(text) {
				t.Errorf("Size = %d, want the tokenizer's %d", n, providertest.Tokens(text))
			}
			if !tc.exact && n != (len(text)+3)/4 {
				t.Errorf("Size = %d, want the estimate of %d for four bytes a token", n, (len(text)+3)/4)
			}
		})
	}
	c := counter.New(providertest.NewTokenServer(providertest.RoutesPrompt))
	if n, exact := c.Size(context.Background(), ""); n != 0 || !exact {
		t.Errorf("Size of nothing = %d, %v; want exactly 0", n, exact)
	}
}

func TestSizeFallsBackToTheEstimateWhenTimeIsUp(t *testing.T) {
	c := counter.New(providertest.NewTokenServer(providertest.RoutesPrompt))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, exact := c.Size(ctx, "remember the milk and call the bank")
	if exact || n == 0 {
		t.Errorf("Size after the deadline = %d, %v; want a non-zero estimate that says it is one", n, exact)
	}
}

func TestPeekSizesARequestWithoutLeavingAPredictionToObserve(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := counter.New(server)
	req := provider.Request{Model: "fake", Tools: toolList, Messages: []provider.Message{provider.UserMessage("hi")}}

	peeked := c.Peek(context.Background(), req)
	if peeked.Method != provider.MethodExact || peeked.Tokens != server.PromptTokens(req) {
		t.Errorf("Peek = %+v, want the exact %d", peeked, server.PromptTokens(req))
	}
	usage := provider.Usage{InputTokens: provider.Reported(server.PromptTokens(req))}
	if _, ok := c.Observe(context.Background(), req, usage); ok {
		t.Error("Observe matched a Peek: a preview must not stand in for the request a run sends")
	}
	predicted, err := c.Predict(context.Background(), req)
	if err != nil || predicted != peeked {
		t.Fatalf("Predict = %+v, %v; want what Peek said", predicted, err)
	}
	if _, ok := c.Observe(context.Background(), req, usage); !ok {
		t.Error("Observe did not match the Predict that followed")
	}
}

// routesThatHonourTheContext is a fake endpoint whose routes give up when
// their context ends, as the HTTP routes do.
type routesThatHonourTheContext struct{ *providertest.TokenServer }

func (r routesThatHonourTheContext) Prompt(ctx context.Context, req provider.Request) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return r.TokenServer.Prompt(ctx, req)
}

func (r routesThatHonourTheContext) Text(ctx context.Context, text string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.TokenServer.Text(ctx, text)
}

func TestPeekAnswersWithTheEstimateWhenTimeIsUp(t *testing.T) {
	c := counter.New(routesThatHonourTheContext{providertest.NewTokenServer(providertest.RoutesPrompt)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := c.Peek(ctx, provider.Request{Model: "fake", Messages: []provider.Message{provider.UserMessage("hi there")}})
	if got.Method != provider.MethodEstimated || got.Tokens == 0 {
		t.Errorf("Peek after the deadline = %+v, want a non-zero estimate", got)
	}
}

func TestImageSizeIsLearnedFromAMeasurement(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := counter.New(server)
	img := provider.Image{MediaType: "image/png", Width: 100, Height: 100, Data: []byte("x")}
	if n, measured := c.ImageSize(img); measured || n != counter.EstimatedImageTokens(100, 100) {
		t.Errorf("ImageSize before any measurement = %d, %v; want the pixel estimate and not measured", n, measured)
	}

	req := provider.Request{Model: "fake", Messages: []provider.Message{{Role: provider.RoleUser, Content: "look", Images: []provider.Image{img}}}}
	if _, err := c.Predict(context.Background(), req); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	usage := provider.Usage{InputTokens: provider.Reported(server.PromptTokens(req))}
	if _, ok := c.Observe(context.Background(), req, usage); !ok {
		t.Fatal("Observe did not match")
	}
	if n, measured := c.ImageSize(img); !measured || n != providertest.ImageTokens(100, 100) {
		t.Errorf("ImageSize after a measurement = %d, %v; want the server's %d and measured", n, measured, providertest.ImageTokens(100, 100))
	}
}
