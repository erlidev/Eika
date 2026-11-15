package subagent_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
	"github.com/erlidev/eika/internal/tool/builtin"
)

func TestQuestionAnswerValidationAndSingleDelivery(t *testing.T) {
	st := newMemory()
	ready := make(chan store.Session, 1)
	answered := make(chan string, 1)
	release := make(chan struct{})
	var s *subagent.Service
	s = service(t, st, func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
		q := builtin.Question{Text: "Which council?", Options: []string{"A", "B"}}
		answer, err := s.Ask(ctx, row.ID, "question-1", q.Text, q.ValidateAnswer, func(_ context.Context, waiting store.Session) { ready <- waiting })
		if err != nil {
			return err
		}
		answered <- answer
		<-release
		return s.Report(ctx, row.ID, subagent.Report{Summary: answer})
	}, &events{})
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	waiting := <-ready
	if waiting.State != store.SessionWaiting || waiting.WaitingReason != "Which council?" {
		t.Fatalf("waiting = %+v", waiting)
	}
	if _, ok, err := s.Message(t.Context(), "home", "plumber", "C", fg, "question-1"); ok || !errors.Is(err, builtin.ErrBadAnswer) {
		t.Fatalf("bad answer: %v, %v", ok, err)
	}
	row, _ := st.Session(t.Context(), waiting.ID)
	if row.State != store.SessionWaiting {
		t.Fatalf("rejected answer changed state: %+v", row)
	}
	st.mu.Lock()
	st.fail = true
	st.mu.Unlock()
	if _, ok, err := s.Message(t.Context(), "home", "plumber", "A", fg, "question-1"); ok || err == nil {
		t.Fatalf("failed state write accepted answer: %v, %v", ok, err)
	}
	st.mu.Lock()
	st.fail = false
	st.mu.Unlock()
	var wg sync.WaitGroup
	accepted := make(chan bool, 12)
	for range 12 {
		wg.Go(func() {
			row, ok, err := s.Message(t.Context(), "home", "plumber", "A", fg, "question-1")
			if err != nil || row.State != store.SessionRunning || row.WaitingReason != "" {
				t.Errorf("answer = %+v, %v", row, err)
			}
			accepted <- ok
		})
	}
	wg.Wait()
	close(accepted)
	count := 0
	for ok := range accepted {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("accepted %d answers", count)
	}
	if answer := <-answered; answer != "A" {
		t.Fatal(answer)
	}
	close(release)
}

func TestQuestionCancelAndCloseJoinTheWait(t *testing.T) {
	for _, closeService := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "close"}[closeService], func(t *testing.T) {
			st := newMemory()
			ready := make(chan struct{})
			var s *subagent.Service
			s = service(t, st, func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
				_, err := s.Ask(ctx, row.ID, "question-1", "Which council?", builtin.Question{}.ValidateAnswer, func(context.Context, store.Session) { close(ready) })
				return err
			}, &events{})
			row, _, err := s.Start(t.Context(), "home", child(), fg)
			if err != nil {
				t.Fatal(err)
			}
			<-ready
			if closeService {
				s.Close()
			} else if _, err := s.Cancel(t.Context(), "home", "plumber"); err != nil {
				t.Fatal(err)
			}
			row, _ = st.Session(t.Context(), row.ID)
			if row.State != store.SessionCancelled || row.WaitingReason != "" {
				t.Fatalf("cancelled = %+v", row)
			}
		})
	}
}

func TestQuestionAnswerAndCancelRace(t *testing.T) {
	for range 40 {
		st := newMemory()
		ready := make(chan struct{})
		release := make(chan struct{})
		var s *subagent.Service
		s = service(t, st, func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
			_, err := s.Ask(ctx, row.ID, "question-1", "Which council?", builtin.Question{}.ValidateAnswer, func(context.Context, store.Session) { close(ready) })
			if err != nil {
				<-release
				return err
			}
			<-ctx.Done()
			<-release
			return ctx.Err()
		}, &events{})
		if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
			t.Fatal(err)
		}
		<-ready
		var wg sync.WaitGroup
		wg.Go(func() {
			_, _, err := s.Message(t.Context(), "home", "plumber", "A", fg, "question-1")
			if err != nil {
				t.Error(err)
			}
			close(release)
		})
		wg.Go(func() {
			if _, err := s.Cancel(t.Context(), "home", "plumber"); err != nil {
				t.Error(err)
			}
		})
		wg.Wait()
		if _, err := s.Cancel(t.Context(), "home", "plumber"); err != nil {
			t.Fatal(err)
		}
		row, _ := st.Subagent(t.Context(), "home", "plumber")
		if row.State != store.SessionCancelled || row.WaitingReason != "" {
			t.Fatalf("state = %+v", row)
		}
		s.Close()
	}
}

func TestQuestionWaitReleasesTheOnlyInferenceSlot(t *testing.T) {
	st := newMemory()
	q := inference.New(nil)
	q.SetPools([]inference.Pool{{Name: "gpu", Slots: 1}})
	ready := make(chan struct{})
	script := providertest.New(
		providertest.Calls("", providertest.Call("ask", "ask_user", map[string]any{"question": "Which council?"})),
		providertest.Text("Done"),
	)
	var s *subagent.Service
	s = service(t, st, func(ctx context.Context, row store.Session, text string, _ inference.Priority) error {
		reg, err := builtin.Registry(builtin.Deps{QuestionRelay: func(ctx context.Context, asked builtin.Question) (string, bool, error) {
			answer, err := s.Ask(ctx, row.ID, "question-1", asked.Text, asked.ValidateAnswer, func(context.Context, store.Session) { close(ready) })
			return answer, true, err
		}})
		if err != nil {
			return err
		}
		loop := agent.New(script, reg, agent.Options{Inference: agent.Inference{Queue: q, Pool: "gpu"}})
		return loop.Run(ctx, &agent.Session{ID: row.ID, Conversation: agent.NewConversation()}, provider.UserMessage(text))
	}, &events{})
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	<-ready
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	lease, err := q.Acquire(ctx, inference.Need{Pool: "gpu", Priority: inference.Background})
	if err != nil {
		t.Fatalf("waiting question held the only slot: %v", err)
	}
	lease.Release()
	if _, ok, err := s.Message(t.Context(), "home", "plumber", "A", fg, "question-1"); err != nil || !ok {
		t.Fatalf("answer: %v, %v", ok, err)
	}
	awaitState(t, st, store.SessionIdle)
	requests := script.Requests()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d", len(requests))
	}
	found := false
	for _, m := range requests[1].Messages {
		if m.Role == provider.RoleTool && m.Content == "A" {
			found = true
		}
	}
	if !found {
		t.Fatal("the answer did not complete the existing tool call")
	}
}

func TestCancelledQuestionCannotTakeAnAnswerWhileItsNoticeIsPosting(t *testing.T) {
	st := newMemory()
	ready := make(chan context.CancelFunc, 1)
	release := make(chan struct{})
	var s *subagent.Service
	s = service(t, st, func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
		questionCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		_, err := s.Ask(questionCtx, row.ID, "question-1", "Which?", builtin.Question{}.ValidateAnswer, func(context.Context, store.Session) { ready <- cancel; <-release })
		return err
	}, &events{})
	if _, _, err := s.Start(t.Context(), "home", child(), fg); err != nil {
		t.Fatal(err)
	}
	cancel := <-ready
	cancel()
	if s.PendingQuestion("question-1") {
		t.Fatal("cancelled question is pending")
	}
	if _, ok, err := s.Message(t.Context(), "home", "plumber", "A", fg, "question-1"); err != nil || ok {
		t.Fatalf("cancelled question accepted answer: %v, %v", ok, err)
	}
	close(release)
}
