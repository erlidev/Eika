package subagent

import (
	"context"
	"fmt"

	"github.com/erlidev/eika/internal/store"
)

type pendingQuestion struct {
	answer chan string
	accept func(string) error
}

// Ask parks the active child with reason until Message supplies an accepted
// answer or ctx ends. Notify runs after the question can take an answer,
// outside the service lock. The caller owns validation and notice delivery.
func (s *Service) Ask(ctx context.Context, id, reason string, accept func(string) error, notify func(context.Context, store.Session)) (string, error) {
	if err := validText("question", reason, 4096); err != nil {
		return "", err
	}
	s.mu.Lock()
	child, err := s.store.Session(ctx, id)
	active := s.active[id]
	if err == nil && (active == nil || active.cancelled || active.question != nil || ctx.Err() != nil) {
		err = fmt.Errorf("subagent has no active run available for a question")
	}
	if err == nil {
		child, err = s.state(ctx, child, store.SessionWaiting, reason)
	}
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	pending := &pendingQuestion{answer: make(chan string, 1), accept: accept}
	active.question = pending
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if active.question == pending {
			active.question = nil
		}
		s.mu.Unlock()
	}()
	notify(ctx, child)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case answer := <-pending.answer:
		return answer, nil
	}
}
