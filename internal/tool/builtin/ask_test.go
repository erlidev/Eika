package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// askUser returns the registered ask_user tool.
func askUser(t *testing.T, q *builtin.Questions) tool.Tool {
	t.Helper()
	r, err := builtin.Registry(builtin.Deps{Questions: q})
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	tl, ok := r.Get("ask_user")
	if !ok {
		t.Fatal("ask_user is not registered")
	}
	return tl
}

// collector records the events a tool emits.
type collector struct {
	events chan event.Event
}

func newCollector() *collector { return &collector{events: make(chan event.Event, 8)} }

func (c *collector) Emit(_ context.Context, e event.Event) { c.events <- e }

// asked waits for the next question.asked payload.
func (c *collector) asked(t *testing.T) event.QuestionAsked {
	t.Helper()
	select {
	case e := <-c.events:
		if e.Type != event.TypeQuestionAsked {
			t.Fatalf("event type = %q, want question.asked", e.Type)
		}
		var payload event.QuestionAsked
		if err := e.DecodePayload(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		return payload
	case <-time.After(2 * time.Second):
		t.Fatal("no question.asked event arrived")
		return event.QuestionAsked{}
	}
}

func TestAskUserBlocksUntilAnswered(t *testing.T) {
	q := builtin.NewQuestions()
	emitter := newCollector()
	tl := askUser(t, q)

	results := make(chan tool.Result, 1)
	go func() {
		res, err := tl.Call(context.Background(), tool.CallContext{
			Emit:      emitter,
			SessionID: "s1",
			RunID:     "run-1",
			CallID:    "call-1",
		}, json.RawMessage(`{"question":"which branch?","options":["main","work"]}`))
		if err != nil {
			t.Errorf("call ask_user: %v", err)
		}
		results <- res
	}()

	payload := emitter.asked(t)
	if payload.Question != "which branch?" || payload.SessionID != "s1" || payload.CallID != "call-1" {
		t.Errorf("payload = %+v", payload)
	}
	if payload.AllowFreeText {
		t.Error("allow_free_text = true, want false for a question with options")
	}
	if pending := q.Pending(); len(pending) != 1 || pending[0].ID != payload.QuestionID {
		t.Fatalf("pending = %+v, want the asked question", pending)
	}
	for _, bad := range []string{"c", ""} {
		if err := q.Answer(payload.QuestionID, bad); !errors.Is(err, builtin.ErrBadAnswer) || len(q.Pending()) != 1 {
			t.Errorf("answer %q = %v, want ErrBadAnswer and the question still pending", bad, err)
		}
	}
	if err := q.Answer(payload.QuestionID, "work"); err != nil {
		t.Fatalf("answer: %v", err)
	}

	select {
	case res := <-results:
		if res.Content != "work" || res.IsError {
			t.Errorf("result = %+v, want the answer", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask_user did not return after the answer")
	}
	if pending := q.Pending(); len(pending) != 0 {
		t.Errorf("pending = %+v, want none after the answer", pending)
	}
}

func TestAskUserStopsWhenTheRunIsCancelled(t *testing.T) {
	q := builtin.NewQuestions()
	emitter := newCollector()
	tl := askUser(t, q)
	ctx, cancel := context.WithCancel(context.Background())

	errs := make(chan error, 1)
	go func() {
		_, err := tl.Call(ctx, tool.CallContext{Emit: emitter, SessionID: "s1"},
			json.RawMessage(`{"question":"stop?"}`))
		errs <- err
	}()

	payload := emitter.asked(t)
	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask_user did not return after cancellation")
	}
	if err := q.Answer(payload.QuestionID, "late"); !errors.Is(err, builtin.ErrNoQuestion) {
		t.Errorf("answering an abandoned question = %v, want ErrNoQuestion", err)
	}
}

func TestARejectedAnswerNeverOutlivesItsRun(t *testing.T) {
	// A rejected answer used to take the question out of the broker and put
	// it back. A run ending in that window left a question nothing would ever
	// remove, and the session reported it as pending for good.
	for range 200 {
		q := builtin.NewQuestions()
		emitter := newCollector()
		tl := askUser(t, q)
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = tl.Call(ctx, tool.CallContext{Emit: emitter, SessionID: "s1"},
				json.RawMessage(`{"question":"which?","options":["a","b"]}`))
		}()
		payload := emitter.asked(t)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			// Not one of the options, so it is refused either way.
			_ = q.Answer(payload.QuestionID, "c")
		}()
		go func() {
			defer wg.Done()
			<-start
			cancel()
		}()
		close(start)
		wg.Wait()
		<-done
		cancel()

		if pending := q.Pending(); len(pending) != 0 {
			t.Fatalf("pending = %+v, want none once the run has ended", pending)
		}
	}
}

func TestAskUserRelaysWithoutRegisteringALegacyQuestion(t *testing.T) {
	q := builtin.NewQuestions()
	reg, err := builtin.Registry(builtin.Deps{Questions: q, QuestionRelay: func(_ context.Context, asked builtin.Question) (string, bool, error) {
		if asked.SessionID != "child" || asked.CallID != "call-1" || asked.Text != "Which finish?" || asked.AllowFreeText {
			t.Errorf("question = %+v", asked)
		}
		if len(q.Pending()) != 0 {
			t.Error("relayed question registered with the HTTP broker")
		}
		if err := q.Answer(asked.ID, "Matte"); !errors.Is(err, builtin.ErrNoQuestion) {
			t.Errorf("HTTP answer = %v", err)
		}
		return "Matte", true, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	tl, _ := reg.Get("ask_user")
	res, err := tl.Call(t.Context(), tool.CallContext{SessionID: "child", CallID: "call-1"}, json.RawMessage(`{"question":"Which finish?","options":["Matte","Gloss"]}`))
	if err != nil || res.IsError || res.Content != "Matte" || len(q.Pending()) != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
}
