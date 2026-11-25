package counter_test

import (
	"context"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
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

func TestSwitchServesTheRoutesOnlyWhileTheyAreAttached(t *testing.T) {
	ctx := context.Background()
	server := providertest.NewTokenServer(providertest.RoutesPrompt)
	var sw counter.Switch
	c := counter.New(&sw)
	req := provider.Request{Model: "fake", Messages: []provider.Message{provider.UserMessage("hello there, how are you")}}

	if pred, err := c.Predict(ctx, req); err != nil || pred.Method != provider.MethodEstimated {
		t.Fatalf("detached: %+v, %v; want an estimate", pred, err)
	}
	release := sw.Attach(server)
	if !sw.Attached() {
		t.Fatal("not attached after Attach")
	}
	if pred, err := c.Predict(ctx, req); err != nil || pred.Method != provider.MethodExact {
		t.Fatalf("attached: %+v, %v; want exact", pred, err)
	}
	release()
	release() // a second call does nothing
	if sw.Attached() {
		t.Error("still attached after release")
	}
	if pred, err := c.Predict(ctx, req); err != nil || pred.Method != provider.MethodEstimated {
		t.Errorf("released: %+v, %v; want an estimate", pred, err)
	}
}

// Two uses at once do not clear each other's routes: they are dropped when the
// last one ends.
func TestSwitchKeepsTheRoutesUntilTheLastUserReleases(t *testing.T) {
	var sw counter.Switch
	first := sw.Attach(providertest.NewTokenServer(providertest.RoutesPrompt))
	second := sw.Attach(providertest.NewTokenServer(providertest.RoutesPrompt))
	first()
	first()
	if !sw.Attached() {
		t.Fatal("the first release dropped the routes the second user holds")
	}
	second()
	if sw.Attached() {
		t.Error("routes kept after every user released")
	}
	third := sw.Attach(providertest.NewTokenServer(providertest.RoutesText))
	defer third()
	if sw.Level() != counter.LevelText {
		t.Errorf("Level = %v, want the routes attached last", sw.Level())
	}
}
