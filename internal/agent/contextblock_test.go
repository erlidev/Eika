package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// stepClock is a clock a test moves. Every reading is the time it was last set
// to, so a test controls exactly which readings the loop takes.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func newStepClock(t time.Time) *stepClock { return &stepClock{t: t} }

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// blockAt is the block a message sent at the given minute of 2026-10-31 UTC
// carries.
func blockAt(hour, minute int) string {
	return "<context>\ntime: Sat 2026-10-31 " + pad(hour) + ":" + pad(minute) + " +00:00 (UTC)\n</context>"
}

func pad(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestEveryUserMessageEndsWithTheTimeItWasSent(t *testing.T) {
	clock := newStepClock(time.Date(2026, 10, 31, 14, 5, 59, 0, time.UTC))
	f := newFixtureWith(t, agent.Options{Clock: clock.Now}, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "bash", map[string]any{"command": "echo hi"})),
		providertest.Text("done"),
		providertest.Text("again"),
	})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("first")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	clock.Advance(2*time.Hour + 30*time.Second)
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("second")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	msgs := f.session.Conversation.Messages()
	var users []provider.Message
	for _, m := range msgs {
		if m.Role == provider.RoleUser {
			users = append(users, m)
			continue
		}
		if m.Context != "" {
			t.Errorf("a %s message carries a context block: %q", m.Role, m.Context)
		}
	}
	if len(users) != 2 {
		t.Fatalf("%d user messages, want 2", len(users))
	}
	if users[0].Context != blockAt(14, 5) || users[1].Context != blockAt(16, 6) {
		t.Errorf("blocks = %q and %q, want the minute each message was sent in", users[0].Context, users[1].Context)
	}
	if users[0].Content != "first" || users[1].Content != "second" {
		t.Errorf("contents = %q and %q: the block is not part of what the user wrote", users[0].Content, users[1].Content)
	}
	if got := users[0].ModelText(); got != "first\n\n"+blockAt(14, 5) {
		t.Errorf("the model reads %q, want the message and the block at its end", got)
	}

	// What the interface shows of the turn is what the user wrote.
	var start event.TurnStart
	f.events.payloadOf(t, event.TypeTurnStart, &start)
	if start.Message != "first" {
		t.Errorf("turn.start message = %q, want %q", start.Message, "first")
	}
}

func TestTheTimeIsWrittenOnceSoThePrefixHoldsWhateverTheClockDoes(t *testing.T) {
	// A block computed when a request is sent would change the bytes of every
	// earlier user message on every turn, and no request would be a prefix of
	// the next. Here the clock moves minutes between turns, a restart rebuilds
	// the session from what was stored, and the prefix still holds.
	clock := newStepClock(time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC))
	steps := []providertest.Step{
		providertest.Calls("looking", providertest.Call("c1", "bash", map[string]any{"command": "echo one"})),
		providertest.Text("one done"),
		providertest.Text("two done"),
		providertest.Text("three done"),
	}
	f := newFixtureWith(t, agent.Options{Clock: clock.Now}, steps)
	run := func(a *agent.Agent, s *agent.Session, text string) {
		t.Helper()
		if err := a.Run(context.Background(), s, provider.UserMessage(text)); err != nil {
			t.Fatalf("Run %q: %v", text, err)
		}
	}
	run(f.agent, f.session, "one")
	clock.Advance(7 * time.Minute)
	run(f.agent, f.session, "two")

	// A restart: a new agent, a session rebuilt from the stored messages.
	clock.Advance(3 * time.Hour)
	restarted := newFixtureWith(t, agent.Options{Clock: clock.Now}, steps[3:])
	resumed := agent.NewSession(f.session.ID, f.session.ComputerID)
	for _, m := range f.store.Messages(f.session.ID) {
		resumed.Conversation.Append(m)
	}
	run(restarted.agent, resumed, "three")

	var serialised []string
	for _, req := range append(f.provider.Requests(), restarted.provider.Requests()...) {
		var b strings.Builder
		for _, m := range req.Messages {
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		serialised = append(serialised, b.String())
	}
	if len(serialised) != 4 {
		t.Fatalf("%d requests, want 4", len(serialised))
	}
	for i := 1; i < len(serialised); i++ {
		if !strings.HasPrefix(serialised[i], serialised[i-1]) {
			t.Errorf("request %d does not begin with request %d:\n%s\nvs\n%s", i+1, i, serialised[i], serialised[i-1])
		}
	}
	if !strings.Contains(serialised[3], "time: Sat 2026-10-31 09:00 ") || !strings.Contains(serialised[3], "time: Sat 2026-10-31 09:07 ") {
		t.Errorf("the last request lost the times its first messages were sent at:\n%s", serialised[3])
	}
}

func TestAQueuedMessageIsStampedWhenItWasSentNotWhenItIsDelivered(t *testing.T) {
	clock := newStepClock(time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC))
	var a *agent.Agent
	var steerAt, followAt time.Time
	queue := callTool{name: "queue", run: func(context.Context) string {
		a.Steer(provider.UserMessage("steer"))
		a.FollowUp(provider.UserMessage("follow"))
		steerAt = clock.Now()
		followAt = clock.Now()
		clock.Advance(45 * time.Minute)
		return "queued"
	}}
	f := newFixtureWith(t, agent.Options{Clock: clock.Now}, []providertest.Step{
		providertest.Calls("", providertest.Call("c1", "queue", map[string]any{})),
		providertest.Text("steered"),
		providertest.Text("followed"),
	}, queue)
	a = f.agent
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("go")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var steer, follow provider.Message
	for _, m := range f.session.Conversation.Messages() {
		switch m.Content {
		case "steer":
			steer = m
		case "follow":
			follow = m
		}
	}
	if steer.Context != blockAt(steerAt.Hour(), steerAt.Minute()) || follow.Context != blockAt(followAt.Hour(), followAt.Minute()) {
		t.Errorf("blocks = %q and %q, want the minute the messages were queued in (10:00)", steer.Context, follow.Context)
	}
}

func TestAMessageThatHasItsBlockKeepsIt(t *testing.T) {
	// A message read back from the store, or given back to its queue by a turn
	// that failed, was sent already: its block is never written again.
	clock := newStepClock(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	f := newFixtureWith(t, agent.Options{Clock: clock.Now}, []providertest.Step{providertest.Text("ok")})
	sent := provider.UserMessage("hello")
	sent.Context = blockAt(8, 15)
	if err := f.agent.Run(context.Background(), f.session, sent); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := f.session.Conversation.Messages()[0].Context; got != blockAt(8, 15) {
		t.Errorf("block = %q, want the one the message was sent with", got)
	}
}

func TestTheBlockIsInTheTimeZoneOfTheOptions(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("no time zone database: %v", err)
	}
	clock := newStepClock(time.Date(2026, 10, 31, 14, 5, 0, 0, time.UTC))
	f := newFixtureWith(t, agent.Options{Clock: clock.Now, Location: berlin}, []providertest.Step{providertest.Text("ok")})
	if err := f.agent.Run(context.Background(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "<context>\ntime: Sat 2026-10-31 15:05 +01:00 (Europe/Berlin)\n</context>"
	if got := f.session.Conversation.Messages()[0].Context; got != want {
		t.Errorf("block = %q, want %q", got, want)
	}
}

func TestACompactionSummaryNeverSeesTheBlockAsTheUsersWords(t *testing.T) {
	// The cached form sends the messages as they were, blocks included, and its
	// prompt says what the blocks are. The transcript form is text the harness
	// writes itself: it holds what the user wrote and no block.
	clock := newStepClock(time.Date(2026, 10, 31, 14, 5, 0, 0, time.UTC))
	for name, prompt := range map[string]string{
		"first summary": agent.SummaryPrompt, "update": agent.UpdatePrompt, "turn prefix": agent.TurnPrefixPrompt,
	} {
		if !strings.Contains(prompt, "<context> block") || !strings.Contains(prompt, "not the user's words") {
			t.Errorf("the %s prompt does not say what a <context> block is: %.200q", name, prompt)
		}
	}

	history := []provider.Message{provider.UserMessage("u0 " + filler("a", 400)), provider.AssistantMessage("a0", nil)}
	for i := range history {
		history[i] = withBlock(history[i], clock)
	}
	text := agent.Transcript(history)
	if strings.Contains(text, "<context>") || strings.Contains(text, "time:") {
		t.Errorf("the transcript form carries a block: %.300q", text)
	}
	if !strings.Contains(text, "[User]: u0 ") {
		t.Errorf("the transcript lost what the user wrote: %.300q", text)
	}
}

func withBlock(m provider.Message, clock *stepClock) provider.Message {
	if m.Role == provider.RoleUser {
		m.Context = blockAt(clock.Now().Hour(), clock.Now().Minute())
	}
	return m
}

func TestTheCounterCountsTheBlockItIsSent(t *testing.T) {
	// A user message is sent with its block, so the prediction counts it, and an
	// exact prediction of a request with blocks has no drift.
	server := providertest.NewTokenServer(providertest.RoutesPrompt, providertest.Text("one"), providertest.Text("two"))
	clock := newStepClock(time.Date(2026, 10, 31, 14, 5, 0, 0, time.UTC))
	log := &callLog{}
	a := agent.New(server, lookTool(t), agent.Options{
		Model: "fake", ContextWindow: 100_000, Clock: clock.Now,
		Counter: counter.New(server), Recorder: log, Logger: quietLogger(),
	})
	s := agent.NewSession("s1", "w1")
	for _, text := range []string{"first", "second"} {
		if err := a.Run(t.Context(), s, provider.UserMessage(text)); err != nil {
			t.Fatalf("Run: %v", err)
		}
		clock.Advance(time.Minute)
	}
	calls := log.all()
	if len(calls) != 2 {
		t.Fatalf("%d calls recorded, want 2", len(calls))
	}
	for i, c := range calls {
		if c.Drift == nil || *c.Drift != 0 || c.Prediction.Method != provider.MethodExact {
			t.Errorf("call %d: %s prediction with drift %v, want exact with none while the blocks are counted", i+1, c.Prediction.Method, deref(c.Drift))
		}
	}
	sent := calls[0].Context.Request()
	bare := sent
	bare.Messages = []provider.Message{provider.UserMessage("first")}
	if got, without := calls[0].Prediction.Tokens, server.PromptTokens(bare); got <= without {
		t.Errorf("predicted %d tokens for a message with its block, no more than the %d of one without", got, without)
	}
}
