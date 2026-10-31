package counter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// The agent lowers a request's max output to the room the window leaves after
// it has predicted the prompt, and observes the request it sent. That request
// differs from the predicted one in that field alone, and the pair must match.
func TestMaxOutputDoesNotSeparateAPredictionFromItsMeasurement(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := counter.New(server)
	ctx := context.Background()
	req := provider.Request{Model: "fake", System: "be brief", Messages: []provider.Message{provider.UserMessage("hello there")}}
	req.Sampling.MaxOutput = new(8192)

	pred, err := c.Predict(ctx, req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	sent := req
	sent.Sampling.MaxOutput = new(500)
	out, ok := c.Observe(ctx, sent, providertest.Usage(pred.Tokens, 5, pred.Tokens+5))
	if !ok || out.Drift != 0 {
		t.Errorf("Observe = %+v, %v; want the prediction found, with no drift", out, ok)
	}
	// Anything else that changes the request is another request.
	other := req
	other.System = "be verbose"
	if _, ok := c.Observe(ctx, other, providertest.Usage(pred.Tokens, 5, pred.Tokens+5)); ok {
		t.Error("Observe matched a request with another system prompt")
	}
}

func TestEstimateAnswersWithoutAskingTheServerAndIsObserved(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	c := counter.New(server)
	req := provider.Request{Model: "fake", Messages: []provider.Message{provider.UserMessage("hello there, how are you today")}}

	pred := c.Estimate(req)
	if pred.Method != provider.MethodEstimated || pred.Tokens <= 0 {
		t.Fatalf("Estimate = %+v, want a positive estimate", pred)
	}
	if server.RenderCalls() != 0 || server.TextCalls() != 0 {
		t.Errorf("Estimate asked the server (%d renders, %d texts)", server.RenderCalls(), server.TextCalls())
	}
	out, ok := c.Observe(context.Background(), req, providertest.Usage(40, 5, 45))
	if !ok || out.Prediction != pred || out.Drift != pred.Tokens-40 {
		t.Errorf("Observe = %+v, %v; want the estimate measured against 40", out, ok)
	}
	if c.EstimateError() <= 0 {
		t.Error("EstimateError = 0 after an estimate was measured")
	}
}

// A session resumed after a restart has a measurement in its stored messages
// and a counter that has measured nothing. Seeding teaches the counter that
// measurement, so an endpoint that counts nothing is sized from it rather than
// from bytes alone, and the error of estimates is untouched.
func TestSeedAnchorsAFreshCounterOnAStoredMeasurement(t *testing.T) {
	server := providertest.NewTokenServer(providertest.RoutesNone)
	c := counter.New(server)
	ctx := context.Background()
	history := provider.Request{Model: "fake", System: "be brief", Messages: []provider.Message{
		provider.UserMessage(strings.Repeat("one question ", 150)), provider.AssistantMessage(strings.Repeat("one answer ", 150), nil),
	}}
	if !c.Seed(ctx, history, providertest.Usage(1000, 20, 1020)) {
		t.Fatal("Seed learned nothing from a fresh counter")
	}
	if c.EstimateError() != 0 {
		t.Errorf("EstimateError = %v after a seed, want it untouched", c.EstimateError())
	}

	next := history
	next.Messages = append(append([]provider.Message(nil), history.Messages...), provider.UserMessage("two more words"))
	pred, err := c.Predict(ctx, next)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if pred.Tokens < 1000 || pred.Tokens > 1030 {
		t.Errorf("prediction %d, want the stored 1000 plus a few tokens for one short message", pred.Tokens)
	}

	if c.Seed(ctx, history, providertest.Usage(5000, 20, 5020)) {
		t.Error("Seed taught a counter that had learned already")
	}
	if c.Seed(context.Background(), history, provider.Usage{}) {
		t.Error("Seed learned from a usage that states no prompt")
	}
}

func TestCountTextTokenisesOnlyWhereTheEndpointCan(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		routes providertest.Routes
		ok     bool
	}{
		{"prompt route", providertest.RoutesPrompt, true},
		{"text route", providertest.RoutesText, true},
		{"no route", providertest.RoutesNone, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := counter.New(providertest.NewTokenServer(tc.routes))
			n, ok := c.CountText(ctx, "thinking about the next step")
			if ok != tc.ok || (ok && n != providertest.Tokens("thinking about the next step")) {
				t.Errorf("CountText = %d, %v; want ok %v", n, ok, tc.ok)
			}
		})
	}
	if _, ok := counter.NewEstimating().CountText(ctx, "anything"); ok {
		t.Error("an estimating counter counted a text")
	}
}

// A provider that cannot state its chat body leaves nothing to render, so a
// server that renders chat requests is used for text alone.
func TestRoutesWithoutAnEncoderCountTextOnly(t *testing.T) {
	routes := counter.NewRoutes(counter.Config{BaseURL: "http://unused", Backend: probe.LlamaCpp, Counting: probe.CountChat}, nil)
	if got := routes.Level(); got != counter.LevelText {
		t.Errorf("Level = %v, want text", got)
	}
}

func TestEstimatingCounterPredictsByBytesAndLearns(t *testing.T) {
	c := counter.NewEstimating()
	req := provider.Request{Model: "fake", Messages: []provider.Message{provider.UserMessage("a question of some length")}}
	pred, err := c.Predict(context.Background(), req)
	if err != nil || pred.Method != provider.MethodEstimated || pred.Tokens <= 0 {
		t.Fatalf("Predict = %+v, %v; want an estimate", pred, err)
	}
}
