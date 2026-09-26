package server

import (
	"context"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/utility"
)

// titleTimeout bounds making one session title. A title that takes longer
// is not worth the wait; the session stays untitled and its next run tries
// again.
const titleTimeout = 2 * time.Minute

// The titles an untitled session shows until its first run names it.
const (
	untitledSession = "New session"
	untitledChat    = "New chat"
)

// titles names untitled sessions in the background, with the model the
// utility_models setting assigns the session title task. A title outlives
// the run that asked for it, so each is a goroutine owned here: the context
// made in newTitles bounds them, and stop cancels it and waits for them
// before the store they write to closes.
type titles struct {
	server *Server
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	stopped bool
	// naming holds the sessions a title is being made for, so a run that
	// starts before the last one's title arrives does not ask again.
	naming map[string]struct{}
}

// newTitles returns the title maker of the server.
func newTitles(s *Server) *titles {
	ctx, cancel := context.WithCancel(context.Background())
	return &titles{server: s, ctx: ctx, cancel: cancel, naming: make(map[string]struct{})}
}

// start names an untitled session after message, its first, in the
// background. It does nothing once stop was called or while the session's
// title is already being made.
func (t *titles) start(sess store.Session, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, busy := t.naming[sess.ID]; busy || t.stopped {
		return
	}
	t.naming[sess.ID] = struct{}{}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.name(sess, message)
		t.mu.Lock()
		delete(t.naming, sess.ID)
		t.mu.Unlock()
	}()
}

// name makes the title and gives it to the session, which leaves the session
// untitled when no model is assigned the task or the model fails.
func (t *titles) name(sess store.Session, message string) {
	s := t.server
	ctx, cancel := context.WithTimeout(t.ctx, titleTimeout)
	defer cancel()
	m, ok, err := s.utilityModel(ctx, utility.TaskSessionTitle)
	if err != nil {
		s.log.Error("read the session title model", "session_id", sess.ID, "error", err)
		return
	}
	if !ok {
		return
	}
	um, err := s.utilityClient(ctx, m)
	if err != nil {
		s.log.Error("build the session title model", "session_id", sess.ID, "model", m.Name, "error", err)
		return
	}
	title, err := utility.Title(ctx, um, message)
	if err != nil {
		s.log.Warn("make session title", "session_id", sess.ID, "model", m.Name, "error", err)
		return
	}
	named, err := s.deps.Store.TitleUntitledSession(ctx, sess.ID, title)
	if err != nil {
		s.log.Error("title session", "session_id", sess.ID, "error", err)
		return
	}
	if !named {
		// Deleted or titled while the model answered.
		return
	}
	e, err := event.New(event.TypeSessionTitle, event.TopicGlobal, event.SessionTitle{
		SessionID:   sess.ID,
		WorkspaceID: sess.WorkspaceID,
		Title:       title,
	})
	if err != nil {
		s.log.Error("encode session title event", "session_id", sess.ID, "error", err)
		return
	}
	s.deps.Bus.Emit(ctx, e)
	s.log.Info("session titled", "session_id", sess.ID, "model", m.Name)
}

// stop cancels every title being made and waits for them. No title starts
// after it.
func (t *titles) stop() {
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
	t.cancel()
	t.wg.Wait()
}

// utilityClient returns model m, as configured, for a utility task.
func (s *Server) utilityClient(ctx context.Context, m store.Model) (utility.Model, error) {
	p, err := s.providerOf(ctx, m)
	if err != nil {
		return utility.Model{}, err
	}
	return utility.Model{
		Provider:         p,
		ID:               m.Model,
		MaxOutput:        m.MaxOutput,
		ReasoningEffort:  m.ReasoningEffort,
		ReasoningEfforts: m.ReasoningEfforts,
		ThinkingSwitch:   provider.ThinkingSwitch(m.ThinkingSwitch),
	}, nil
}

// firstMessage returns the first user message of a conversation, or text
// when it has none yet: text is then the message the run starts with.
func firstMessage(messages []provider.Message, text string) string {
	for _, m := range messages {
		if m.Role == provider.RoleUser {
			return m.Content
		}
	}
	return text
}
