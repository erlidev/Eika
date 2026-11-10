package agent_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// roughCounter returns a counter whose estimates have been off by a quarter of
// the measured prompt or more, again and again. Each request is new, so none
// extends the last, and the server reads them alternately as 40% larger and
// 20% smaller than four bytes a token, which leaves the learned ratio near
// four and the estimates wrong each time.
func roughCounter(t *testing.T) *counter.Counter {
	t.Helper()
	c := counter.NewEstimating()
	ctx := context.Background()
	for i := range 12 {
		req := provider.Request{Model: "m", Messages: []provider.Message{provider.UserMessage(fmt.Sprintf("%d %s", i, strings.Repeat("x", 4000)))}}
		if _, err := c.Predict(ctx, req); err != nil {
			t.Fatalf("Predict: %v", err)
		}
		measured := 1400
		if i%2 == 1 {
			measured = 800
		}
		c.Observe(ctx, req, providertest.Usage(measured, 10, measured+10))
	}
	if got := c.EstimateError(); got < 0.2 {
		t.Fatalf("EstimateError = %v, the fixture needs estimates that are far off", got)
	}
	return c
}

func TestOnlyAnEstimatedPredictionWidensTheCompactionReserve(t *testing.T) {
	// A 12,000 token window caps the reserve at 3,000: the threshold is 9,000.
	const window = 12_000
	rough := roughCounter(t)
	tests := []struct {
		name string
		pred provider.Prediction
		c    *counter.Counter
		auto bool
		want bool
	}{
		{"exact below the threshold", provider.Prediction{Tokens: 8_000, Method: provider.MethodExact}, rough, true, false},
		{"exact above the threshold", provider.Prediction{Tokens: 9_100, Method: provider.MethodExact}, rough, true, true},
		{"anchored is trusted to its number", provider.Prediction{Tokens: 8_000, Method: provider.MethodAnchored}, rough, true, false},
		{"estimated with no error shown is the plain threshold", provider.Prediction{Tokens: 8_000, Method: provider.MethodEstimated}, counter.NewEstimating(), true, false},
		{"estimated with a large error shown compacts earlier", provider.Prediction{Tokens: 8_000, Method: provider.MethodEstimated}, rough, true, true},
		{"estimated far below even the widened reserve", provider.Prediction{Tokens: 3_000, Method: provider.MethodEstimated}, rough, true, false},
		{"without automatic compaction nothing compacts", provider.Prediction{Tokens: 11_000, Method: provider.MethodEstimated}, rough, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := agent.New(providertest.New(), nil, agent.Options{
				ContextWindow: window, Compaction: agent.CompactionSettings{Auto: tc.auto}, Counter: tc.c,
			})
			if got := a.ShouldCompact(tc.pred); got != tc.want {
				t.Errorf("ShouldCompact(%+v) = %t, want %t", tc.pred, got, tc.want)
			}
		})
	}
}

func TestWidenedReserveNeverPassesHalfTheWindow(t *testing.T) {
	cases := []struct {
		name                  string
		reserve, window, size int
		relativeError         float64
		want                  int
	}{
		{"no error shown", 3_000, 12_000, 8_000, 0, 3_000},
		{"a tenth of the request", 3_000, 12_000, 8_000, 0.1, 3_800},
		{"rounded up", 1_000, 100_000, 999, 0.5, 1_500},
		{"capped at half the window", 3_000, 12_000, 8_000, 0.9, 6_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agent.WidenedReserve(tc.reserve, tc.window, tc.size, tc.relativeError); got != tc.want {
				t.Errorf("WidenedReserve = %d, want %d", got, tc.want)
			}
		})
	}
}

// Through the loop: eight messages of 1,000 tokens are under the 9,000 token
// threshold of a 12,000 token window, and an endpoint that tokenises nothing
// compacts them anyway once its estimates have been shown to run low, while
// the same conversation with no error shown yet is sent as it is.
func TestAnEstimatedEndpointCompactsEarlierByTheErrorItHasShown(t *testing.T) {
	cases := []struct {
		name         string
		counter      *counter.Counter
		wantRequests int
	}{
		{"no estimate has been measured", counter.NewEstimating(), 1},
		{"estimates have run low", roughCounter(t), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := providertest.New(providertest.Text("SUMMARY"), providertest.Text("answer"))
			c := newCompactor(p, compactorOptions{
				window: 12_000, counter: tc.counter,
				compaction: agent.CompactionSettings{Auto: true, KeepRecentTokens: 2000},
			})
			s := sessionOf(history(8, 1000))
			if err := c.agent.Run(context.Background(), s, provider.UserMessage("next")); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := p.Calls(); got != tc.wantRequests {
				t.Errorf("made %d requests, want %d", got, tc.wantRequests)
			}
			compacted := len(c.store.compactions["s1"]) == 1
			if compacted != (tc.wantRequests == 2) || (compacted && c.store.compactions["s1"][0].Reason != event.CompactThreshold) {
				t.Errorf("stored compactions = %+v", c.store.compactions["s1"])
			}
		})
	}
}

// The window is judged by the number the counter gives, not by bytes: a text
// of many one-letter words is two bytes and one token a word, so four bytes to
// a token would call it half its size and miss the threshold.
func TestTheWindowIsJudgedByTheCountersNumber(t *testing.T) {
	text := strings.Repeat("a ", 4000)
	run := func(c *counter.Counter, p provider.Provider) (*compactor, error) {
		cp := newCompactor(p, compactorOptions{
			window: 12_000, counter: c,
			compaction: agent.CompactionSettings{Auto: true, KeepRecentTokens: 1000},
		})
		s := sessionOf([]provider.Message{
			provider.UserMessage(text), provider.AssistantMessage(text, nil), provider.UserMessage(text),
		})
		return &cp, cp.agent.Run(context.Background(), s, provider.UserMessage("next"))
	}

	// By bytes the conversation is about 6,000 tokens and is sent as it is.
	estimating := providertest.New(providertest.Text("answer"))
	if _, err := run(counter.NewEstimating(), estimating); err != nil || estimating.Calls() != 1 {
		t.Fatalf("estimated: %d calls, %v; want the one call, no compaction", estimating.Calls(), err)
	}

	// The server reads about 12,000 and the loop compacts first.
	server := providertest.NewTokenServer(providertest.RoutesPrompt, providertest.Text("SUMMARY"), providertest.Text("answer"))
	c, err := run(counter.New(server), server)
	if err != nil {
		t.Fatalf("counted: %v", err)
	}
	if got := c.store.compactions["s1"]; len(got) != 1 || got[0].TokensBefore < 11_000 {
		t.Errorf("stored compactions = %+v, want one compaction from a request the server reads as over 11,000 tokens", got)
	}
}
