//go:build docker

package server_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// heldCall holds the at-th call of its script until release closes, after
// telling reached, which keeps a turn going while the test acts on it.
type heldCall struct {
	provider.Provider
	at      int32
	calls   atomic.Int32
	reached chan struct{}
	release chan struct{}
}

func newHeldCall(at int32, p provider.Provider) *heldCall {
	return &heldCall{Provider: p, at: at, reached: make(chan struct{}, 1), release: make(chan struct{})}
}

func (g *heldCall) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	if g.calls.Add(1) == g.at {
		g.reached <- struct{}{}
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.Provider.Stream(ctx, req)
}

// homeNotices returns the notice entries of the home session by id, with
// their text, and how often stored user messages delivered each one.
func (d delegating) homeNotices(t *testing.T) (texts map[string]string, deliveries map[string]int) {
	t.Helper()
	entries, err := d.store.Entries(t.Context(), d.home.ID)
	if err != nil {
		t.Fatal(err)
	}
	texts, deliveries = map[string]string{}, map[string]int{}
	for _, e := range entries {
		switch e.Kind {
		case store.KindNotice:
			text, err := session.TextOf(e)
			if err != nil {
				t.Fatal(err)
			}
			texts[e.ID] = text
		case store.KindUser:
			m, _, err := session.Message(e)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range m.Notices {
				deliveries[id]++
			}
		}
	}
	return texts, deliveries
}

// lastUser returns the newest user-side message of a request.
func lastUser(req provider.Request) provider.Message {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == provider.RoleUser {
			return req.Messages[i]
		}
	}
	return provider.Message{}
}

// homeCalls returns the home session's recorded model calls, oldest first.
func (d delegating) homeCalls(t *testing.T) []modelCallWire {
	t.Helper()
	return decodeBody[callsWire](t, request(t, d.Server, "GET", "/api/sessions/"+d.home.ID+"/calls", nil), 200).Calls
}

// settled waits until home has no run and every notice is delivered.
func (d delegating) settled(t *testing.T) {
	t.Helper()
	waitFor(t, "every notice to be delivered", func() bool {
		undelivered, err := d.store.UndeliveredNotices(t.Context(), d.home.ID)
		return err == nil && len(undelivered) == 0 && !d.runState(t, d.home.ID).Active
	})
}

func TestAReportBecomesANoticeThatABackgroundTurnDelivers(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(
		call("start", "subagent_start", map[string]any{"agent": "researcher", "name": "flights", "brief": "Find flights"}),
		providertest.Text("on it"),
		providertest.Text("hello"),
	)
	researcher := newHeldScript(
		call("done", "report", map[string]any{"summary": "Tuesday is cheapest", "follow_ups": []string{"book Tuesday"}}),
		providertest.Text("reported"),
	)
	notices := answeringNotices(eika)
	d.use(byAgent{"You are Eika": notices, "You are a researcher": researcher})

	d.postMessage(t, d.home.ID, "find flights", "", 202)
	<-researcher.started
	d.waitIdle(t, d.home.ID)
	researcher.open()
	waitFor(t, "the background turn", func() bool { return len(notices.Requests()) == 1 })
	d.settled(t)

	texts, deliveries := d.homeNotices(t)
	want := "flights (researcher) reported: Tuesday is cheapest\n\nFollow-ups:\n- book Tuesday"
	var id string
	for key, text := range texts {
		id = key
		if text != want {
			t.Errorf("notice text = %q, want %q", text, want)
		}
	}
	if len(texts) != 1 || deliveries[id] != 1 {
		t.Fatalf("notices %v delivered %v, want one notice delivered once", texts, deliveries)
	}
	sent := lastUser(notices.Requests()[0])
	if sent.Content != "" || !strings.Contains(sent.Context, "\nnotice: flights (researcher) reported: Tuesday is cheapest\n\n  Follow-ups:\n  - book Tuesday\n") {
		t.Errorf("background turn sent %q with context %q", sent.Content, sent.Context)
	}
	calls := d.homeCalls(t)
	if got := calls[len(calls)-1].Priority; got != inference.Background.String() {
		t.Errorf("background turn priority = %q, want background", got)
	}

	// The next turn the user starts does not deliver it again.
	d.postMessage(t, d.home.ID, "hi", "", 202)
	d.waitIdle(t, d.home.ID)
	if got := lastUser(eika.Requests()[2]); got.Content != "hi" || strings.Contains(got.Context, "notice:") {
		t.Errorf("the user's next message = %+v, want no notice", got)
	}
	if _, deliveries := d.homeNotices(t); deliveries[id] != 1 {
		t.Errorf("deliveries = %v, want one", deliveries)
	}
	if got := calls[0].Priority; got != inference.Interactive.String() {
		t.Errorf("the user's turn priority = %q, want interactive", got)
	}
}

func TestANoticeThatFindsHomeBusyWaitsForTheRunsNextMessageOrItsEnd(t *testing.T) {
	for _, steer := range []bool{true, false} {
		name := "the run ends"
		if steer {
			name = "the user steers"
		}
		t.Run(name, func(t *testing.T) {
			d := newDelegating(t)
			// Eika's second call is held while the researcher reports.
			eika := newHeldCall(2, providertest.New(
				call("start", "subagent_start", map[string]any{"agent": "researcher", "name": "flights", "brief": "Find flights"}),
				providertest.Text("on it"),
				providertest.Text("answered"),
			))
			researcher := providertest.New(call("done", "report", map[string]any{"summary": "Tuesday"}), providertest.Text("ok"))
			notices := answeringNotices(eika)
			d.use(byAgent{"You are Eika": notices, "You are a researcher": researcher})

			run := d.postMessage(t, d.home.ID, "find flights", "", 202)
			<-eika.reached
			d.waitState(t, "flights", store.SessionIdle)
			waitFor(t, "the notice", func() bool { texts, _ := d.homeNotices(t); return len(texts) == 1 })
			if state := d.runState(t, d.home.ID); state.Run == nil || state.Run.ID != run.ID {
				t.Fatalf("home run = %+v, want the user's run still going", state.Run)
			}
			if steer {
				d.postMessage(t, d.home.ID, "and?", "steer", 202)
			}
			close(eika.release)
			d.waitIdle(t, d.home.ID)
			d.settled(t)

			texts, deliveries := d.homeNotices(t)
			for id := range texts {
				if deliveries[id] != 1 {
					t.Errorf("notice delivered %d times, want once", deliveries[id])
				}
			}
			if steer {
				if len(notices.Requests()) != 0 {
					t.Errorf("%d background turns, want none: the steered message delivered the notice", len(notices.Requests()))
				}
				reqs := eika.Provider.(*providertest.Provider).Requests()
				if got := lastUser(reqs[2]); got.Content != "and?" || !strings.Contains(got.Context, "notice: flights (researcher) reported: Tuesday") {
					t.Errorf("steered message = %+v, want the notice", got)
				}
			} else if len(notices.Requests()) != 1 {
				t.Errorf("%d background turns, want one after the run ended", len(notices.Requests()))
			}
		})
	}
}

func TestSimultaneousReportsAreEachDeliveredOnce(t *testing.T) {
	d := newDelegating(t)
	names := []string{"one", "two", "three"}
	var steps []providertest.Step
	for _, name := range names {
		steps = append(steps, call("start-"+name, "subagent_start", map[string]any{"agent": "researcher", "name": name, "brief": "Look into " + name}))
	}
	eika := providertest.New(append(steps, providertest.Text("started"))...)
	researchers := byTask{}
	for _, name := range names {
		researchers["Look into "+name] = providertest.New(call("done", "report", map[string]any{"summary": name + " done"}), providertest.Text("ok"))
	}
	notices := answeringNotices(eika)
	d.use(byAgent{"You are Eika": notices, "You are a researcher": researchers})

	d.postMessage(t, d.home.ID, "look into three things", "", 202)
	waitFor(t, "the subagents", func() bool {
		rows, err := d.store.Subagents(t.Context(), d.home.ID)
		return err == nil && len(rows) == len(names)
	})
	for _, name := range names {
		d.waitState(t, name, store.SessionIdle)
	}
	d.settled(t)
	texts, deliveries := d.homeNotices(t)
	if len(texts) != len(names) {
		t.Fatalf("%d notices, want %d", len(texts), len(names))
	}
	var said []string
	for id, text := range texts {
		said = append(said, text)
		if deliveries[id] != 1 {
			t.Errorf("%q delivered %d times, want once", text, deliveries[id])
		}
	}
	slices.Sort(said)
	if want := []string{"one (researcher) reported: one done", "three (researcher) reported: three done", "two (researcher) reported: two done"}; !slices.Equal(said, want) {
		t.Errorf("notices = %q, want %q", said, want)
	}
}

func TestAnUndeliveredNoticeFromAnEarlierProcessReachesTheNextTurn(t *testing.T) {
	d := newDelegating(t)
	// A notice the last process stored and never delivered.
	e, err := session.TextEntry(store.KindNotice, "plumber (worker) reported: Found one")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := d.store.AppendEntry(t.Context(), d.home.ID, e)
	if err != nil {
		t.Fatal(err)
	}
	eika := providertest.New(providertest.Text("Plumber found one."), providertest.Text("hello"))
	d.use(byAgent{"You are Eika": eika})

	d.postMessage(t, d.home.ID, "anything new?", "", 202)
	d.waitIdle(t, d.home.ID)
	d.postMessage(t, d.home.ID, "hi", "", 202)
	d.waitIdle(t, d.home.ID)
	reqs := eika.Requests()
	if got := lastUser(reqs[0]); !strings.Contains(got.Context, "notice: plumber (worker) reported: Found one") || !slices.Equal(got.Notices, []string{stored.ID}) {
		t.Errorf("first message = %+v, want the stored notice", got)
	}
	if got := lastUser(reqs[1]); strings.Contains(got.Context, "notice:") {
		t.Errorf("second message = %+v, want no notice", got)
	}
}

func TestAMessageDuringABackgroundTurnJoinsItAtInteractivePriority(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(
		call("start", "subagent_start", map[string]any{"agent": "researcher", "name": "flights", "brief": "Find flights"}),
		providertest.Text("on it"),
		providertest.Text("Flights are cheapest on Tuesday."),
	)
	researcher := newHeldScript(call("done", "report", map[string]any{"summary": "Tuesday"}), providertest.Text("ok"))
	notices := answeringNotices(eika)
	notices.arrived, notices.release = make(chan struct{}, 1), make(chan struct{})
	d.use(byAgent{"You are Eika": notices, "You are a researcher": researcher})

	d.postMessage(t, d.home.ID, "find flights", "", 202)
	<-researcher.started
	d.waitIdle(t, d.home.ID)
	researcher.open()
	<-notices.arrived
	background := d.runState(t, d.home.ID).Run
	if background == nil {
		t.Fatal("no background run")
	}
	// The client did not know of the turn and asks for a run of its own.
	joined := d.postMessage(t, d.home.ID, "what did it find?", "", 202)
	if joined.ID != background.ID {
		t.Errorf("the message started run %s, want it to join %s", joined.ID, background.ID)
	}
	close(notices.release)
	d.waitIdle(t, d.home.ID)

	reqs := eika.Requests()
	if got := lastUser(reqs[2]); got.Content != "what did it find?" {
		t.Errorf("the joined turn answered %q", got.Content)
	}
	var priorities []string
	for _, c := range d.homeCalls(t) {
		if c.RunID != "" {
			priorities = append(priorities, c.Priority)
		}
	}
	if n := len(priorities); n < 2 || priorities[n-2] != inference.Background.String() || priorities[n-1] != inference.Interactive.String() {
		t.Errorf("priorities = %v, want the notice call background and the user's interactive", priorities)
	}
}

// turnstile holds every call until the test lets one through, after telling
// arrived.
type turnstile struct {
	*providertest.Provider
	arrived chan struct{}
	pass    chan struct{}
}

func newTurnstile(steps ...providertest.Step) *turnstile {
	return &turnstile{Provider: providertest.New(steps...), arrived: make(chan struct{}, 8), pass: make(chan struct{}, 8)}
}

func (g *turnstile) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	g.arrived <- struct{}{}
	select {
	case <-g.pass:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.Provider.Stream(ctx, req)
}

func TestANoticeTheNextRunTakesAsTheLastRunEndsIsDeliveredOnce(t *testing.T) {
	d := newDelegating(t)
	eika := newTurnstile(providertest.Text("one"), providertest.Text("two"), providertest.Text("three"), providertest.Text("four"))
	d.use(byAgent{"You are Eika": eika})
	// The moment the first run has released home, a second run starts and
	// takes the waiting notice into its message before the first run goes on.
	var once sync.Once
	joined := make(chan int, 1)
	d.AfterRunReleasesForTest(func(id string) {
		if id != d.home.ID {
			return
		}
		once.Do(func() {
			joined <- request(t, d.Server, "POST", "/api/sessions/"+d.home.ID+"/messages", map[string]any{"text": "hi"}).Code
			<-eika.arrived
		})
	})

	d.postMessage(t, d.home.ID, "first", "", 202)
	<-eika.arrived
	if err := d.PostNoticeForTest(t.Context(), d.home.ID, "plumber (worker) reported: Found one"); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		eika.pass <- struct{}{}
	}
	if code := <-joined; code != 202 {
		t.Fatalf("second message = %d", code)
	}
	d.settled(t)
	// A notice wrongly put back would ride on this message, or on a
	// background turn before it.
	d.postMessage(t, d.home.ID, "bye", "", 202)
	d.waitIdle(t, d.home.ID)
	d.settled(t)

	texts, deliveries := d.homeNotices(t)
	for id := range texts {
		if deliveries[id] != 1 {
			t.Errorf("notice delivered %d times, want once", deliveries[id])
		}
	}
	if got := lastUser(eika.Requests()[1]); got.Content != "hi" || len(got.Notices) != 1 {
		t.Errorf("the second run's message = %+v, want it to carry the notice", got)
	}
}

func TestAMessageWhileABackgroundTurnIsBuiltJoinsIt(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(providertest.Text("It found one."))
	d.use(byAgent{"You are Eika": answeringNotices(eika)})
	// Building the background turn's provider takes a while, and the user's
	// message arrives meanwhile.
	d.delay = 300 * time.Millisecond
	if err := d.PostNoticeForTest(t.Context(), d.home.ID, "plumber (worker) reported: Found one"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the background turn", func() bool { return d.runState(t, d.home.ID).Active })
	joined := d.postMessage(t, d.home.ID, "what's new?", "", 202)
	d.waitIdle(t, d.home.ID)

	runs, err := d.store.Runs(t.Context(), d.home.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != joined.ID {
		t.Errorf("runs = %+v, want the message to join the one background turn %s", runs, joined.ID)
	}
	if reqs := eika.Requests(); len(reqs) != 1 || lastUser(reqs[0]).Content != "what's new?" {
		t.Errorf("Eika's requests = %d, want the user's message answered in the background turn", len(reqs))
	}
}

func TestAReportWhoseNoticeFailsToWriteIsWrittenLater(t *testing.T) {
	d := newDelegating(t)
	d.FailNoticeWritesForTest(2, 10*time.Millisecond)
	eika := providertest.New(
		call("start", "subagent_start", map[string]any{"agent": "researcher", "name": "flights", "brief": "Find flights"}),
		providertest.Text("on it"),
	)
	researcher := providertest.New(call("done", "report", map[string]any{"summary": "Tuesday"}), providertest.Text("ok"))
	notices := answeringNotices(eika)
	d.use(byAgent{"You are Eika": notices, "You are a researcher": researcher})

	d.postMessage(t, d.home.ID, "find flights", "", 202)
	d.waitIdle(t, d.home.ID)
	d.waitState(t, "flights", store.SessionIdle)
	waitFor(t, "the notice to be written", func() bool { texts, _ := d.homeNotices(t); return len(texts) == 1 })
	waitFor(t, "the background turn", func() bool { return len(notices.Requests()) == 1 })
	d.settled(t)
	texts, deliveries := d.homeNotices(t)
	for id, text := range texts {
		if text != "flights (researcher) reported: Tuesday" || deliveries[id] != 1 {
			t.Errorf("notice %q delivered %d times, want once", text, deliveries[id])
		}
	}
}
