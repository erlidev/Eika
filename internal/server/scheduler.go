package server

import (
	"context"
	"fmt"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/schedule"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

type scheduleRunner struct{ server *Server }

func (r scheduleRunner) RunRoutine(ctx context.Context, routine schedule.Routine) (string, string, string) {
	s := r.server
	sess := store.Session{Kind: store.SessionRoutine, Agent: routine.Agent, Name: routine.Name, Title: routine.Name, ProjectID: routine.ProjectID}
	if routine.Agent == "eika" || routine.Agent == "worker" {
		home, err := s.deps.Store.HomeComputer(ctx)
		if err != nil {
			return "", "error", err.Error()
		}
		sess.ComputerID = home.ID
	}
	sess, err := s.deps.Store.CreateSession(ctx, sess)
	if err != nil {
		return "", "error", err.Error()
	}
	if err = s.deps.Store.AttachRoutineSession(ctx, routine.RunID, sess.ID); err != nil {
		return sess.ID, "error", err.Error()
	}
	_, active, err := s.runs.launch(ctx, sess.ID, "", job{message: provider.UserMessage(routine.Prompt), priority: inference.Background})
	if err != nil {
		return sess.ID, "error", err.Error()
	}
	select {
	case <-active.done:
	case <-ctx.Done():
		active.abort()
		<-active.done
	}
	if err = active.outcome(); err != nil {
		return sess.ID, "error", err.Error()
	}
	result, err := s.finalMessage(ctx, sess)
	if err != nil {
		return sess.ID, "error", err.Error()
	}
	home, err := s.deps.Store.HomeSession(ctx, store.LocalOwner, "home")
	if err != nil {
		return sess.ID, "error", err.Error()
	}
	s.notices.postSoon(ctx, home.ID, "routine-"+routine.RunID, fmt.Sprintf("%s: %s", routine.Name, result), nil)
	s.notifications.send("A routine reported")
	return sess.ID, "done", ""
}

func (r scheduleRunner) RunWakeup(ctx context.Context, wakeup schedule.Wakeup) error {
	s := r.server
	sess, err := s.deps.Store.Session(ctx, wakeup.SessionID)
	if err != nil {
		return err
	}
	if sess.Agent != wakeup.Agent {
		return fmt.Errorf("wakeup agent does not match session")
	}
	message := fmt.Sprintf("<context>wakeup: %s, scheduled %s</context>", wakeup.Note, wakeup.DueAt.UTC().Format(time.RFC3339))
	_, active, err := s.runs.launch(ctx, sess.ID, "", job{message: provider.UserMessage(message), priority: inference.Background, wakeupID: wakeup.ID})
	if err != nil {
		return err
	}
	select {
	case <-active.done:
	case <-ctx.Done():
		active.abort()
		<-active.done
	}
	return active.outcome()
}

// wakeupStore commits the first user message with the wakeup's state.
type wakeupStore struct {
	agent.Store
	db      *store.Store
	id      string
	channel bool
}

func (s *wakeupStore) Append(ctx context.Context, sessionID, runID string, m provider.Message) error {
	if m.Role != provider.RoleUser || s.id == "" {
		return s.Store.Append(ctx, sessionID, runID, m)
	}
	e, err := session.MessageEntry(m)
	if err != nil {
		return err
	}
	e.RunID = runID
	if s.channel {
		e.Channel = "web"
	}
	err = s.db.AppendWakeup(ctx, s.id, sessionID, e)
	if err == nil {
		s.id = ""
	}
	return err
}

// recoverRoutineNotices makes completed results available after a restart.
func (s *Server) recoverRoutineNotices(ctx context.Context) error {
	results, err := s.deps.Store.CompletedRoutines(ctx)
	if err != nil {
		return err
	}
	home, err := s.deps.Store.HomeSession(ctx, store.LocalOwner, "home")
	if err != nil {
		return err
	}
	for _, result := range results {
		text, err := s.finalMessage(ctx, store.Session{ID: result.SessionID})
		if err != nil {
			return err
		}
		entry, err := session.TextEntry(store.KindNotice, fmt.Sprintf("%s: %s", result.Name, text))
		if err != nil {
			return err
		}
		entry.ID = "routine-" + result.RunID
		if _, _, err = s.deps.Store.AppendEntryOnce(ctx, home.ID, entry); err != nil {
			return err
		}
	}
	return nil
}
