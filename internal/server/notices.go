package server

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// notices delivers harness news, such as a subagent's report, to the home
// session. A notice is a notice entry in the session first, so it survives a
// restart, and is delivered by the next user-side message the session sends:
// its <context> block carries the text and its stored payload lists the entry,
// which is what marks the notice delivered. A notice that finds the session
// idle starts a background turn to deliver it; one that finds it busy waits
// for the next message of the run, or for the run to end.
//
// The book keeps in memory which notices are waiting and which a message has
// taken but not yet stored. It reads a session's undelivered notices from the
// store the first time a run of the session needs them.
type notices struct {
	server *Server
	// ctx bounds the background turns the book starts; close cancels it.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// ioMu orders reading a session's notices from the store with writing a
	// new one, so that a notice is neither missed nor read twice.
	ioMu sync.Mutex

	mu       sync.Mutex
	closed   bool
	sessions map[string]*sessionNotices
}

// sessionNotices are one home session's undelivered notices, in the order
// they were written.
type sessionNotices struct {
	// waiting are in no message yet.
	waiting []agent.Notice
	// held are in a message that is not stored yet: queued, or in flight.
	held []agent.Notice
}

func newNotices(s *Server) *notices {
	ctx, cancel := context.WithCancel(context.Background())
	return &notices{server: s, ctx: ctx, cancel: cancel, sessions: make(map[string]*sessionNotices)}
}

// post writes a notice to a home session and makes sure a turn delivers it.
func (n *notices) post(ctx context.Context, sessionID, text string) error {
	e, err := session.TextEntry(store.KindNotice, text)
	if err != nil {
		return err
	}
	n.ioMu.Lock()
	added, err := n.server.tree.Append(ctx, sessionID, e)
	if err == nil {
		n.mu.Lock()
		if book := n.sessions[sessionID]; book != nil {
			book.waiting = append(book.waiting, agent.Notice{ID: added.ID, Text: text})
		}
		n.mu.Unlock()
	}
	n.ioMu.Unlock()
	if err != nil {
		return fmt.Errorf("post notice: %w", err)
	}
	n.server.log.Info("notice posted", "session_id", sessionID, "entry_id", added.ID)
	n.wake(sessionID)
	return nil
}

// load reads a session's undelivered notices from the store, once.
func (n *notices) load(ctx context.Context, sessionID string) error {
	n.ioMu.Lock()
	defer n.ioMu.Unlock()
	n.mu.Lock()
	loaded := n.sessions[sessionID] != nil
	n.mu.Unlock()
	if loaded {
		return nil
	}
	entries, err := n.server.deps.Store.UndeliveredNotices(ctx, sessionID)
	if err != nil {
		return err
	}
	book := &sessionNotices{}
	for _, e := range entries {
		text, err := session.TextOf(e)
		if err != nil {
			return err
		}
		book.waiting = append(book.waiting, agent.Notice{ID: e.ID, Text: text})
	}
	n.mu.Lock()
	n.sessions[sessionID] = book
	n.mu.Unlock()
	return nil
}

// waiting reports whether a session has notices no message has taken.
func (n *notices) waiting(sessionID string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	book := n.sessions[sessionID]
	return book != nil && len(book.waiting) > 0
}

// take hands the waiting notices to the message being stamped. It is the
// agent's Notices option for the session's runs.
func (n *notices) take(sessionID string) []agent.Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	book := n.sessions[sessionID]
	if book == nil || len(book.waiting) == 0 {
		return nil
	}
	taken := book.waiting
	book.waiting = nil
	book.held = append(book.held, taken...)
	return taken
}

// delivered records that a stored message delivered the notices it lists.
func (n *notices) delivered(sessionID string, ids []string) {
	if len(ids) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if book := n.sessions[sessionID]; book != nil {
		book.held = slices.DeleteFunc(book.held, func(x agent.Notice) bool { return slices.Contains(ids, x.ID) })
	}
}

// settle returns the notices of a run that ended to the waiting list, except
// those still in a message the next run receives, and reports whether any
// are waiting.
func (n *notices) settle(sessionID string, queued queuedMessages) bool {
	var kept []string
	for _, m := range slices.Concat(queued.steering, queued.followUps) {
		kept = append(kept, m.Notices...)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	book := n.sessions[sessionID]
	if book == nil {
		return false
	}
	var back []agent.Notice
	book.held = slices.DeleteFunc(book.held, func(x agent.Notice) bool {
		if slices.Contains(kept, x.ID) {
			return false
		}
		back = append(back, x)
		return true
	})
	book.waiting = append(back, book.waiting...)
	return len(book.waiting) > 0
}

// wake starts a background turn on a session that has waiting notices and
// no run. A session with a run is left alone: the run delivers the notices
// with its next message, or wakes the session again when it ends.
func (n *notices) wake(sessionID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return
	}
	n.wg.Go(func() {
		_, _, err := n.server.runs.launch(n.ctx, sessionID, "", job{
			message: provider.Message{Role: provider.RoleUser}, notify: true, priority: inference.Background,
		})
		switch {
		case err == nil:
		case isConflict(err), errors.Is(err, errNothingToDeliver):
			// A run is going, or another turn took the notices first.
		default:
			n.server.log.Warn("start background turn for notices", "session_id", sessionID, "error", err)
		}
	})
}

// close stops starting background turns and waits for those being started.
// Notices written after it wait in the store for the next run.
func (n *notices) close() {
	n.mu.Lock()
	n.closed = true
	n.mu.Unlock()
	n.cancel()
	n.wg.Wait()
}

// errNothingToDeliver is what a background turn's launch returns when the
// notices it was started for were delivered by another turn first.
var errNothingToDeliver = errors.New("no notices to deliver")

// noticeStore records a home session's messages and tells the book which
// notices each stored message delivered.
type noticeStore struct {
	*session.Store
	notices *notices
}

// Append records one message and, once it is stored, the notices it carried.
func (s noticeStore) Append(ctx context.Context, sessionID string, m provider.Message) error {
	if err := s.Store.Append(ctx, sessionID, m); err != nil {
		return err
	}
	s.notices.delivered(sessionID, m.Notices)
	return nil
}

// reportNotice is the notice a subagent's report becomes in Eika's context.
func reportNotice(child store.Session) string {
	return fmt.Sprintf("%s (%s) reported: %s", child.Name, child.Agent, child.Result)
}

// subagentReported posts a child's report to its home session as a notice.
// It is the lifecycle service's Reported option.
func (s *Server) subagentReported(ctx context.Context, child store.Session) {
	if err := s.notices.post(ctx, child.ParentSessionID, reportNotice(child)); err != nil {
		s.log.Error("post subagent report notice", "session_id", child.ID, "error", err)
	}
}
