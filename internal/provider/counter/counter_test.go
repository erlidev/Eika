package counter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// session plays a conversation against a fake server the way the agent loop
// does: predict, send, observe, append what came back.
type session struct {
	t        *testing.T
	server   *providertest.TokenServer
	counter  *counter.Counter
	req      provider.Request
	outcomes []counter.Outcome
}

func newSession(t *testing.T, server *providertest.TokenServer) *session {
	t.Helper()
	return &session{
		t: t, server: server, counter: counter.New(server),
		req: provider.Request{
			Model:            "fake",
			System:           "You are a careful assistant that keeps answers short.",
			PreserveThinking: true,
			Tools: []provider.ToolDef{
				{Name: "ls", Description: "list a directory", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
				{Name: "read", Description: "read a file", Schema: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
			},
		},
	}
}

// send predicts the current request, sends it, and observes the measurement.
// The answer is added to the conversation.
func (s *session) send() counter.Outcome {
	s.t.Helper()
	ctx := context.Background()
	pred, err := s.counter.Predict(ctx, s.req)
	if err != nil {
		s.t.Fatalf("Predict: %v", err)
	}
	events, err := s.server.Stream(ctx, s.req)
	if err != nil {
		s.t.Fatalf("Stream: %v", err)
	}
	var usage provider.Usage
	var text strings.Builder
	var calls []provider.ToolCall
	for e := range events {
		switch e.Kind {
		case provider.KindUsage:
			usage = e.Usage
		case provider.KindTextDelta:
			text.WriteString(e.Text)
		case provider.KindToolCall:
			calls = append(calls, e.ToolCall)
		}
	}
	out, ok := s.counter.Observe(ctx, s.req, usage)
	if !ok {
		s.t.Fatal("Observe did not match the prediction")
	}
	if out.Prediction != pred {
		s.t.Fatalf("Observe returned prediction %+v, Predict said %+v", out.Prediction, pred)
	}
	s.outcomes = append(s.outcomes, out)
	reasoning := fmt.Sprintf("thinking about step %d of the work", len(s.outcomes))
	s.req.Messages = append(s.req.Messages, provider.AssistantMessageWithReasoning(text.String(), reasoning, calls))
	return out
}

func (s *session) user(text string) {
	s.req.Messages = append(s.req.Messages, provider.UserMessage(text))
}

func (s *session) toolResults(calls []provider.ToolCall, results ...string) {
	for i, r := range results {
		s.req.Messages = append(s.req.Messages, provider.ToolResultMessage(calls[i].ID, r, false))
	}
}

// compact replaces the conversation with a summary, as a compaction does.
func (s *session) compact(summary string) {
	m := provider.UserMessage(summary)
	m.Summary = true
	s.req.Messages = []provider.Message{m}
}

// script is a conversation with every kind of message in it and a compaction
// near the end.
func script(s *session) {
	call := func(id, name, args string) provider.ToolCall {
		return provider.ToolCall{ID: id, Name: name, Arguments: provider.ToolArguments(args)}
	}
	ls := call("c1", "ls", `{"path":"/home/eika"}`)
	read := call("c2", "read", `{"path":"/home/eika/notes.md"}`)
	ls2 := call("c3", "ls", `{"path":"/tmp"}`)

	s.user("hello, what is in my home directory?")
	s.send()
	s.user("please list it and then read the notes file")
	s.send()
	s.toolResults([]provider.ToolCall{ls, read}, "notes.md  todo.txt  projects", "remember the milk and call the bank on monday")
	s.send()
	s.user("thanks, and what is in the tmp directory")
	s.send()
	s.toolResults([]provider.ToolCall{ls2}, "a.log b.log")
	s.send()
	s.user("summarise everything for me in two sentences please")
	s.send()
	s.user("one more question about the bank, what did the notes say?")
	s.send()
	s.compact("The user asked about their home directory, a notes file and the tmp directory.")
	s.user("carry on from there and be brief")
	s.send()
	s.user("is there anything else I should do today")
	s.send()
	s.user("and tomorrow")
	s.send()
}

// scriptedServer answers the script's calls: tool calls where the script
// feeds back tool results, text everywhere else.
func scriptedServer(routes providertest.Routes) *providertest.TokenServer {
	call := func(id, name, args string) provider.ToolCall {
		return provider.ToolCall{ID: id, Name: name, Arguments: provider.ToolArguments(args)}
	}
	ls := call("c1", "ls", `{"path":"/home/eika"}`)
	read := call("c2", "read", `{"path":"/home/eika/notes.md"}`)
	ls2 := call("c3", "ls", `{"path":"/tmp"}`)
	return providertest.NewTokenServer(routes,
		providertest.Text("Your home directory holds a few things."),
		providertest.Calls("Listing and reading now.", ls, read),
		providertest.Text("The notes say to remember the milk and call the bank."),
		providertest.Calls("", ls2),
		providertest.Text("The tmp directory holds two log files."),
		providertest.Text("You have notes, a todo list, projects and two logs."),
		providertest.Text("The notes say to call the bank on monday."),
		providertest.Text("Understood, carrying on briefly."),
		providertest.Text("Call the bank and buy milk."),
		providertest.Text("Tomorrow is free."),
	)
}

func TestExactPredictionHasNoDriftOverAConversation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		routes providertest.Routes
	}{
		{"prompt route", providertest.RoutesPrompt},
		{"whole request route", providertest.RoutesRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSession(t, scriptedServer(tc.routes))
			script(s)
			for i, o := range s.outcomes {
				if o.Prediction.Method != provider.MethodExact {
					t.Errorf("call %d: method %s, want exact", i+1, o.Prediction.Method)
				}
				if o.Drift != 0 {
					t.Errorf("call %d: predicted %d, measured %d", i+1, o.Prediction.Tokens, o.Measured)
				}
			}
			if len(s.outcomes) != 10 {
				t.Fatalf("%d calls made, want 10", len(s.outcomes))
			}
		})
	}
}

func TestAnchoredPredictionLearnsTheTemplateAndSurvivesACompaction(t *testing.T) {
	s := newSession(t, scriptedServer(providertest.RoutesText))
	script(s)

	first := s.outcomes[0]
	if first.Prediction.Method != provider.MethodEstimated {
		t.Errorf("first call: method %s, want estimated: nothing has been measured", first.Prediction.Method)
	}
	// The weights of the template are learned from the first few calls; after
	// that every prediction is exact to the token, across the compaction too
	// (the call after the summary is call 8).
	const warmUp = 6
	for i, o := range s.outcomes {
		if i >= 1 && o.Prediction.Method != provider.MethodAnchored {
			t.Errorf("call %d: method %s, want anchored", i+1, o.Prediction.Method)
		}
		if i >= 1 && i < warmUp && float64(abs(o.Drift)) > 0.2*float64(o.Measured) {
			t.Errorf("call %d while learning: predicted %d, measured %d, more than 20%% off", i+1, o.Prediction.Tokens, o.Measured)
		}
		if i >= warmUp && o.Drift != 0 {
			t.Errorf("call %d: predicted %d, measured %d, want no drift once learned", i+1, o.Prediction.Tokens, o.Measured)
		}
	}
	for _, i := range []int{7, 8, 9} {
		if o := s.outcomes[i]; o.Drift != 0 {
			t.Errorf("call %d after the compaction: drift %d", i+1, o.Drift)
		}
	}
}

func TestEstimatedPredictionLearnsTheRatioAndReportsItsError(t *testing.T) {
	s := newSession(t, scriptedServer(providertest.RoutesNone))
	script(s)

	for i, o := range s.outcomes {
		if o.Prediction.Method != provider.MethodEstimated {
			t.Errorf("call %d: method %s, want estimated: the server tokenises nothing", i+1, o.Prediction.Method)
		}
	}
	if s.counter.EstimateError() <= 0 {
		t.Error("EstimateError = 0 after estimated predictions were measured")
	}
	// An estimate that is off by more than half would not be worth showing.
	last := s.outcomes[len(s.outcomes)-1]
	if rel := float64(abs(last.Drift)) / float64(last.Measured); rel > 0.5 {
		t.Errorf("last estimate %d against %d measured: %.0f%% off", last.Prediction.Tokens, last.Measured, rel*100)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func TestRepeatedPrefixIsNotTokenisedAgain(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := counter.New(server)
	ctx := context.Background()
	req := provider.Request{Model: "fake", System: "You are a careful assistant."}

	// A long conversation of 60 messages, counted as it grows.
	filler := strings.Repeat("the quick brown fox jumps over the lazy dog ", 20)
	var perCall []int
	for i := 0; i < 30; i++ {
		req.Messages = append(req.Messages, provider.UserMessage(fmt.Sprintf("question %d: %s", i, filler)))
		before := server.TextBytes()
		if _, err := c.Predict(ctx, req); err != nil {
			t.Fatalf("Predict: %v", err)
		}
		perCall = append(perCall, server.TextBytes()-before)
		req.Messages = append(req.Messages, provider.AssistantMessage(fmt.Sprintf("answer %d: %s", i, filler), nil))
	}
	whole := len(server.Render(req))
	// Every call after the first tokenises what was added since the last one:
	// an answer and a question, not the conversation.
	for i := 1; i < len(perCall); i++ {
		if perCall[i] > 4*len(filler) {
			t.Fatalf("call %d tokenised %d bytes of a conversation of %d; the added messages are about %d",
				i+1, perCall[i], whole, 2*len(filler))
		}
	}
	if perCall[0] < len(filler) {
		t.Errorf("first call tokenised %d bytes, want the whole first prompt", perCall[0])
	}

	// The same request again costs no request to the server at all.
	if _, err := c.Predict(ctx, req); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	renders, bytes := server.RenderCalls(), server.TextBytes()
	again, err := c.Predict(ctx, req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if server.RenderCalls() != renders || server.TextBytes() != bytes {
		t.Error("an unchanged request was rendered or tokenised again")
	}
	if want := providertest.Tokens(server.Render(req)); again.Tokens != want {
		t.Errorf("prediction %d, want %d", again.Tokens, want)
	}
}

// uncutServer reports cuts in the middle of words, where tokenising in pieces
// changes the count.
type uncutServer struct{ *providertest.TokenServer }

func (u uncutServer) Boundaries(_ context.Context, prompt string) ([]int, error) {
	return []int{len(prompt) / 3, len(prompt) / 2}, nil
}

func TestCuttingThatChangesTheCountIsNotTrusted(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := counter.New(uncutServer{server})
	req := provider.Request{Model: "fake", Messages: []provider.Message{
		provider.UserMessage("abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz"),
	}}
	for i := 0; i < 3; i++ {
		pred, err := c.Predict(context.Background(), req)
		if err != nil {
			t.Fatalf("Predict: %v", err)
		}
		if want := providertest.Tokens(server.Render(req)); pred.Tokens != want {
			t.Errorf("prediction %d, want %d: a cut inside a word must not change the count", pred.Tokens, want)
		}
		req.Messages = append(req.Messages, provider.AssistantMessage("a reply of some length", nil), provider.UserMessage("and more words to count"))
	}
}

func TestImagesAreNotClaimedExact(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt,
		providertest.Text("a picture"), providertest.Text("another"), providertest.Text("again"))
	s := &session{t: t, server: server, counter: counter.New(server),
		req: provider.Request{Model: "fake", System: "You describe pictures."}}
	image := func(w, h int) provider.Message {
		m := provider.UserMessage("what is this?")
		m.Images = []provider.Image{{MediaType: "image/png", Data: []byte("png"), Width: w, Height: h}}
		return m
	}

	s.req.Messages = []provider.Message{image(100, 50)}
	first := s.send()
	if first.Prediction.Method == provider.MethodExact {
		t.Fatal("a request with a picture was called exact; the server's routes do not count pictures")
	}
	if first.Prediction.Method != provider.MethodEstimated || first.Drift == 0 {
		t.Errorf("first picture: %+v, want an estimate that is off", first)
	}

	// The cost of a size is learned from the measurement, and that size is then
	// counted to the token.
	s.req.Messages = append(s.req.Messages, provider.UserMessage("and now?"), image(100, 50))
	second := s.send()
	if second.Prediction.Method != provider.MethodAnchored || second.Drift != 0 {
		t.Errorf("second picture of the same size: %+v, want anchored with no drift", second)
	}

	// A size not seen before is an estimate again.
	s.req.Messages = append(s.req.Messages, provider.UserMessage("and a bigger one?"), image(300, 200))
	third := s.send()
	if third.Prediction.Method != provider.MethodEstimated {
		t.Errorf("new picture size: method %s, want estimated", third.Prediction.Method)
	}
}

func TestPredictFallsBackWhenTheRoutesFail(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := counter.New(failingServer{server})
	pred, err := c.Predict(context.Background(), provider.Request{
		Model: "fake", Messages: []provider.Message{provider.UserMessage("hello there")},
	})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if pred.Method != provider.MethodEstimated || pred.Tokens <= 0 {
		t.Errorf("prediction %+v, want an estimate when the server cannot count", pred)
	}
}

type failingServer struct{ *providertest.TokenServer }

func (failingServer) Prompt(context.Context, provider.Request) (string, error) {
	return "", fmt.Errorf("connection refused")
}

func TestAPredictionCountsTheContextBlockAUserMessageIsSentWith(t *testing.T) {
	block := "<context>\ntime: Sat 2026-10-31 14:05 +00:00 (UTC)\n</context>"
	for _, tc := range []struct {
		name   string
		routes providertest.Routes
		method provider.Method
	}{
		{"a server that renders", providertest.RoutesPrompt, provider.MethodExact},
		{"a server that counts nothing", providertest.RoutesNone, provider.MethodEstimated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := providertest.NewTokenServer(tc.routes)
			plain := provider.Request{Model: "fake", Messages: []provider.Message{provider.UserMessage("what is in my home directory?")}}
			stamped := provider.Request{Model: "fake", Messages: []provider.Message{plain.Messages[0]}}
			stamped.Messages[0].Context = block

			predict := func(req provider.Request) provider.Prediction {
				p, err := counter.New(server).Predict(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			without, with := predict(plain), predict(stamped)
			if with.Method != tc.method || with.Tokens <= without.Tokens {
				t.Errorf("with the block %+v, without %+v: want %s and more tokens with it", with, without, tc.method)
			}
			if tc.method == provider.MethodExact && with.Tokens != server.PromptTokens(stamped) {
				t.Errorf("predicted %d, the server reads %d", with.Tokens, server.PromptTokens(stamped))
			}
		})
	}
}
