package server

import (
	"context"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/utility"
)

// titleTimeout bounds making one session title. A title that takes longer
// is not worth the wait; the session stays untitled and its next run tries
// again.
const titleTimeout = 2 * time.Minute

// titles names untitled sessions in the background, with the model of the
// fast class. A title outlives the run that asked for it, so each is a
// goroutine owned here: the context made in newTitles bounds them, and stop
// cancels it and waits for them before the store they write to closes.
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
// untitled when no model serves titles or the model fails.
func (t *titles) name(sess store.Session, message string) {
	s := t.server
	m, ok, err := s.titleModel(t.ctx)
	if err != nil {
		s.log.Error("read the session title model", "session_id", sess.ID, "error", err)
		return
	}
	if !ok {
		return
	}
	um, row, err := s.utilityClient(t.ctx, m)
	if err != nil {
		s.log.Error("build the session title model", "session_id", sess.ID, "model", m.Name, "error", err)
		return
	}
	title, err := t.ask(sess, row, um, message)
	if err != nil {
		if t.ctx.Err() == nil {
			s.log.Warn("make session title", "session_id", sess.ID, "model", m.Name, "error", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.ctx, titleTimeout)
	defer cancel()
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
		SessionID:  sess.ID,
		ComputerID: sess.ComputerID,
		Title:      title,
	})
	if err != nil {
		s.log.Error("encode session title event", "session_id", sess.ID, "error", err)
		return
	}
	s.deps.Bus.Emit(ctx, e)
	s.log.Info("session titled", "session_id", sess.ID, "model", m.Name)
}

// ask has the model of the fast class write the title. A title is
// maintenance, so it waits in the queue for an idle pool, and nothing bounds
// that wait but the server stopping and the aging that lifts it out of
// maintenance after a while: the run that wanted the title may be long over
// by then, and the title is still made. The time limit starts when the title
// is admitted, since waiting for the pool is not the model being slow.
func (t *titles) ask(sess store.Session, row store.Provider, model utility.Model, message string) (string, error) {
	need := callNeed(row, inference.Maintenance, string(classFast), utility.TitleTokens(model, message))
	need.SessionID = sess.ID
	var title string
	err := t.server.queue(t.ctx).Do(t.ctx, need, func(ctx context.Context, _ *inference.Lease) error {
		ctx, cancel := context.WithTimeout(ctx, titleTimeout)
		defer cancel()
		var err error
		title, err = utility.Title(ctx, model, message)
		return err
	})
	return title, err
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

// titleModel returns the model that makes session titles: the fast class. A
// missing fast model must not turn titles off, so the class falls back to
// main, and main to the default model. A title is not worth the cost of a hosted reasoning model, so titles
// are skipped when the fallback lands on the model the user assigned the deep
// class and that model is on a hosted API. A fast model the user assigned
// outright is used whatever it is.
func (s *Server) titleModel(ctx context.Context) (store.Model, bool, error) {
	models, err := s.deps.Store.Models(ctx)
	if err != nil {
		return store.Model{}, false, err
	}
	assigned := s.classAssignments(ctx)
	choice, ok := resolveClass(classFast, assigned, models, s.defaultModelName(ctx, models))
	if !ok {
		return store.Model{}, false, nil
	}
	if choice.fellBack(classFast) && choice.Model.Name == assigned[classDeep] {
		p, err := s.deps.Store.Provider(ctx, choice.Model.ProviderID)
		if err != nil {
			return store.Model{}, false, err
		}
		if hostedEndpoint(p) {
			return store.Model{}, false, nil
		}
	}
	return choice.Model, true, nil
}

// utilityClient returns model m, as configured, for a utility task, with the
// provider row it runs on.
func (s *Server) utilityClient(ctx context.Context, m store.Model) (utility.Model, store.Provider, error) {
	ep, err := s.endpointOf(ctx, m)
	if err != nil {
		return utility.Model{}, store.Provider{}, err
	}
	return utility.Model{
		Provider:         ep.client,
		ID:               m.Model,
		MaxOutput:        m.MaxOutput,
		ReasoningEffort:  m.ReasoningEffort,
		ReasoningEfforts: m.ReasoningEfforts,
		ThinkingSwitch:   provider.ThinkingSwitch(m.ThinkingSwitch),
	}, ep.row, nil
}

// firstMessage returns the text of the first user message of a
// conversation that has any, or text when none has yet: text is then the
// message the run starts with. A message of images alone names nothing.
func firstMessage(messages []provider.Message, text string) string {
	for _, m := range messages {
		if m.Role == provider.RoleUser && m.Content != "" {
			return m.Content
		}
	}
	return text
}
