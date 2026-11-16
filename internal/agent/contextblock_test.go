package agent_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
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

// noticeBox hands out its notices once, as the server's book does.
type noticeBox struct {
	mu      sync.Mutex
	waiting []agent.Notice
	asked   int
}

func (b *noticeBox) take() []agent.Notice {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asked++
	out := b.waiting
	b.waiting = nil
	return out
}

func TestTheNextUserMessageCarriesTheNoticesAndListsThem(t *testing.T) {
	clock := newStepClock(time.Date(2026, 10, 31, 14, 5, 0, 0, time.UTC))
	box := &noticeBox{waiting: []agent.Notice{
		{ID: "n1", Text: "plumber (worker) reported: Found one\n\nFollow-ups:\n- book Bob"},
		{ID: "n2", Text: "flights (researcher) reported: Tuesday"},
	}}
	f := newFixtureWith(t, agent.Options{Clock: clock.Now, Channel: "web", Notices: box.take},
		[]providertest.Step{providertest.Text("noted"), providertest.Text("hello")})
	if err := f.agent.Run(t.Context(), f.session, provider.UserMessage("hi")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := f.agent.Run(t.Context(), f.session, provider.UserMessage("again")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	users := f.store.Messages(f.session.ID)
	want := "<context>\ntime: Sat 2026-10-31 14:05 +00:00 (UTC)\nchannel: web\n" +
		"notice: plumber (worker) reported: Found one\n\n  Follow-ups:\n  - book Bob\n" +
		"notice: flights (researcher) reported: Tuesday\n</context>"
	if got := users[0].Context; got != want {
		t.Errorf("block = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(users[0].Notices, []string{"n1", "n2"}) {
		t.Errorf("stored notices = %v, want both ids", users[0].Notices)
	}
	// Each notice is delivered once: the next message has none.
	if got := users[2]; got.Content != "again" || strings.Contains(got.Context, "notice:") || got.Notices != nil {
		t.Errorf("the next message = %+v, want no notices", got)
	}
	if box.asked != 2 {
		t.Errorf("notices asked for %d times, want once per message", box.asked)
	}
}
