package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

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

	// write appends a notice entry with its own id once, and reports whether
	// it added it; a test replaces it to make writes fail.
	write func(context.Context, string, store.Entry) (bool, error)
	// retry is the first wait before writing a notice again after a failed
	// write; each further wait doubles, up to maxNoticeRetry.
	retry time.Duration

	// ioMu orders reading a session's notices from the store with writing a
	// new one, so that a notice is neither missed nor read twice.
	ioMu sync.Mutex

	mu       sync.Mutex
	closed   bool
	sessions map[string]*sessionNotices
	// snapshotSeq is, per home session, the last memory change its current
	// snapshot includes.
	snapshotSeq map[string]int64
}

// sessionNotices are one home session's undelivered notices, in the order
// they were written.
type sessionNotices struct {
	// waiting are in no message yet.
	waiting []agent.Notice
	// held are in a message that is not stored yet: queued, or in flight.
	held []agent.Notice
}

// Bounds of writing a notice again after its write failed.
const (
	firstNoticeRetry = time.Second
	maxNoticeRetry   = time.Minute
	noticeWriteLimit = 30 * time.Second
)

func newNotices(s *Server) *notices {
	ctx, cancel := context.WithCancel(context.Background())
	n := &notices{server: s, ctx: ctx, cancel: cancel, retry: firstNoticeRetry, sessions: make(map[string]*sessionNotices), snapshotSeq: make(map[string]int64)}
	n.write = func(ctx context.Context, sessionID string, e store.Entry) (bool, error) {
		_, added, err := n.server.deps.Store.AppendEntryOnce(ctx, sessionID, e)
		return added, err
	}
	return n
}

// post writes a notice with the entry id id to a home session and makes sure
// a turn delivers it. The id names what the notice is about, so writing the
// same notice again, as a retry after a lost response does, adds no entry.
func (n *notices) post(ctx context.Context, sessionID, id, text string, question *session.QuestionNotice) error {
	var e store.Entry
	var err error
	if question != nil {
		e, err = session.QuestionNoticeEntry(text, *question)
	} else {
		e, err = session.TextEntry(store.KindNotice, text)
	}
	if err != nil {
		return err
	}
	e.ID = id
	n.ioMu.Lock()
	err = n.record(ctx, sessionID, e, text)
	n.ioMu.Unlock()
	if err != nil {
		return fmt.Errorf("post notice: %w", err)
	}
	n.server.log.Info("notice posted", "session_id", sessionID, "entry_id", id)
	n.wake(sessionID)
	return nil
}

// record writes a notice entry and adds it to the waiting list of a loaded
// book. A notice an earlier attempt wrote is added only if no stored
// message has delivered it and the book does not have it yet. Callers hold
// ioMu.
func (n *notices) record(ctx context.Context, sessionID string, e store.Entry, text string) error {
	added, err := n.write(ctx, sessionID, e)
	if err != nil {
		return err
	}
	if !added {
		undelivered, err := n.server.deps.Store.UndeliveredNotices(ctx, sessionID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(undelivered, func(u store.Entry) bool { return u.ID == e.ID }) {
			return nil
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	book := n.sessions[sessionID]
	if book == nil {
		return nil
	}
	known := func(x agent.Notice) bool { return x.ID == e.ID }
	if !slices.ContainsFunc(book.waiting, known) && !slices.ContainsFunc(book.held, known) {
		book.waiting = append(book.waiting, agent.Notice{ID: e.ID, Text: text})
	}
	return nil
}

// postSoon posts a notice, and when the write fails keeps writing it again
// in the background, waiting longer each time, until it is written or the
// book closes. The first attempt runs on ctx, so a notice posted while the
// server shuts down is still written.
func (n *notices) postSoon(ctx context.Context, sessionID, id, text string, question *session.QuestionNotice) {
	first, cancel := context.WithTimeout(ctx, noticeWriteLimit)
	err := n.post(first, sessionID, id, text, question)
	cancel()
	if err == nil {
		return
	}
	n.server.log.Warn("post notice; trying again", "session_id", sessionID, "error", err)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		n.server.log.Error("notice pending startup recovery", "session_id", sessionID, "error", err)
		return
	}
	n.wg.Go(func() {
		for wait := n.retry; ; wait = min(2*wait, maxNoticeRetry) {
			timer := time.NewTimer(wait)
			select {
			case <-n.ctx.Done():
				timer.Stop()
				n.server.log.Error("notice pending startup recovery", "session_id", sessionID)
				return
			case <-timer.C:
			}
			attempt, cancel := context.WithTimeout(n.ctx, noticeWriteLimit)
			err := n.post(attempt, sessionID, id, text, question)
			cancel()
			if err == nil {
				return
			}
			n.server.log.Warn("post notice; trying again", "session_id", sessionID, "error", err)
		}
	})
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

// waiting reports whether a session has notices no message has taken that
// are worth a turn of their own. A memory notice is not: it rides with the
// next message and never wakes the session (decision 6).
func (n *notices) waiting(sessionID string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	book := n.sessions[sessionID]
	return book != nil && slices.ContainsFunc(book.waiting, func(x agent.Notice) bool { return !isMemoryNotice(x.ID) })
}

// memoryNoticePrefix starts the entry id of a memory notice; the rest is the
// number of the last memory change the notice names.
const memoryNoticePrefix = "notice-memory-"

func isMemoryNotice(id string) bool { return strings.HasPrefix(id, memoryNoticePrefix) }

// maxMemoryNoticeNames bounds the changes one notice lists.
const maxMemoryNoticeNames = 20

// setSnapshotSeq records the last memory change the session's snapshot
// includes: when a run starts, and when a compaction replaces the snapshot.
func (n *notices) setSnapshotSeq(sessionID string, seq int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.snapshotSeq[sessionID] = seq
}

// collectMemoryChanges runs as a message is stamped. A failure is logged and
// leaves the changes for the next message, which finds them again.
func (n *notices) collectMemoryChanges(sessionID string) {
	n.mu.Lock()
	seq := n.snapshotSeq[sessionID]
	n.mu.Unlock()
	ctx, cancel := context.WithTimeout(n.ctx, noticeWriteLimit)
	defer cancel()
	if err := n.memoryChanges(ctx, sessionID, seq); err != nil {
		n.server.log.Warn("name memory changes; trying at the next message", "session_id", sessionID, "error", err)
	}
}

// memoryChanges writes a notice for the memory changes made since the
// session's snapshot (snapshotSeq) that no earlier notice names, and adds it
// to the session's waiting list without waking the session. Nothing is kept
// in memory between calls: the last change a notice names is in its id, so a
// restart, a retry after a failed write, or a compaction that moved the
// snapshot past some changes neither loses nor repeats one. The notice names
// the memories and what happened to them, never their text.
func (n *notices) memoryChanges(ctx context.Context, sessionID string, snapshotSeq int64) error {
	n.ioMu.Lock()
	defer n.ioMu.Unlock()
	last, err := n.server.deps.Store.LastMemoryNoticeSeq(ctx, sessionID)
	if err != nil {
		return err
	}
	changes, err := n.server.deps.Store.MemoryChangesAfter(ctx, max(last, snapshotSeq))
	if err != nil || len(changes) == 0 {
		return err
	}
	text := memoryNoticeText(changes)
	e, err := session.TextEntry(store.KindNotice, text)
	if err != nil {
		return err
	}
	e.ID = fmt.Sprintf("%s%d", memoryNoticePrefix, changes[len(changes)-1].Seq)
	return n.record(ctx, sessionID, e, text)
}

// memoryNoticeText lists changes, oldest first, by name.
func memoryNoticeText(changes []store.MemoryChange) string {
	var parts []string
	for i, c := range changes {
		if i == maxMemoryNoticeNames {
			parts = append(parts, fmt.Sprintf("and %d more", len(changes)-i))
			break
		}
		what := c.Action
		if c.Action != "purged" && c.State == "pending" {
			what += " (pending review)"
		}
		parts = append(parts, what+" "+c.Name)
	}
	return "Memory changed since your snapshot: " + strings.Join(parts, "; ") + ". Your memory section above is unchanged until the next compaction; use memory_read or memory_search for current content."
}

// take hands the waiting notices to the message being stamped. It is the
// agent's Notices option for the session's runs.
func (n *notices) take(sessionID string) []agent.Notice {
	n.mu.Lock()
	book := n.sessions[sessionID]
	if book == nil || len(book.waiting) == 0 {
		n.mu.Unlock()
		return nil
	}
	taken := book.waiting
	book.waiting = nil
	book.held = append(book.held, taken...)
	n.mu.Unlock()
	return taken
}

// refresh marks questions that ended after a message was stamped but before
// it was sent. The id stays in the message so delivery is recorded once.
func (n *notices) refresh(sessionID string, m provider.Message) provider.Message {
	if len(m.Notices) == 0 || !strings.HasSuffix(m.Context, "</context>") {
		return m
	}
	n.mu.Lock()
	var held []agent.Notice
	if book := n.sessions[sessionID]; book != nil {
		held = slices.Clone(book.held)
	}
	n.mu.Unlock()
	stale := make(map[string]bool)
	for _, id := range m.Notices {
		question := strings.HasPrefix(id, "question-")
		legacy := slices.ContainsFunc(held, func(notice agent.Notice) bool {
			first, _, _ := strings.Cut(notice.Text, "\n")
			return notice.ID == id && strings.Contains(first, ") asks: ") && strings.HasSuffix(notice.Text, "\nAnswer with subagent_message, or ask the user in your reply and relay the answer.")
		})
		stale[id] = legacy || (question && (n.server.subagents == nil || !n.server.subagents.PendingQuestion(id)))
	}
	lines := strings.Split(m.Context, "\n")
	out := make([]string, 0, len(lines))
	index := 0
	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.HasPrefix(line, "notice: ") && index < len(m.Notices) {
			id := m.Notices[index]
			index++
			if stale[id] {
				out = append(out, fmt.Sprintf("notice: Subagent question %s is no longer pending. Do not answer it.", id))
				i++
				for i < len(lines) && (lines[i] == "" || strings.HasPrefix(lines[i], "  ")) {
					i++
				}
				continue
			}
		}
		out = append(out, line)
		i++
	}
	m.Context = strings.Join(out, "\n")
	return m
}

// delivered records that a stored message delivered the notices it lists,
// wherever the book has them.
func (n *notices) delivered(sessionID string, ids []string) {
	if len(ids) == 0 {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if book := n.sessions[sessionID]; book != nil {
		gone := func(x agent.Notice) bool { return slices.Contains(ids, x.ID) }
		book.held = slices.DeleteFunc(book.held, gone)
		book.waiting = slices.DeleteFunc(book.waiting, gone)
	}
}

// settle returns the notices of a run that ended to the waiting list, except
// those still in a message the next run receives. It runs before the run
// releases its session, while no other run can hold notices of the session.
func (n *notices) settle(sessionID string, queued queuedMessages) {
	var kept []string
	for _, m := range slices.Concat(queued.steering, queued.followUps) {
		kept = append(kept, m.Notices...)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	book := n.sessions[sessionID]
	if book == nil {
		return
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
	agent.Store
	notices     *notices
	notifyReply bool
}

// Append records one message and, once it is stored, the notices it carried.
func (s noticeStore) Append(ctx context.Context, sessionID, runID string, m provider.Message) error {
	if err := s.Store.Append(ctx, sessionID, runID, m); err != nil {
		return err
	}
	s.notices.delivered(sessionID, m.Notices)
	if s.notifyReply && m.Role == provider.RoleAssistant && m.Content != "" && len(m.ToolCalls) == 0 {
		s.notices.server.notifications.send("Eika replied")
	}
	return nil
}

// reportNotice is the notice a subagent's report becomes in Eika's context.
func reportNotice(child store.Session) string {
	return fmt.Sprintf("%s (%s) reported: %s", child.Name, child.Agent, child.Result)
}

// reportNoticeID is the entry id of the notice a child's report becomes: the
// stored report id, or the child and a historical entry when looking up a
// notice written before migration 0016.
func reportNoticeID(child store.Session) string {
	if child.ReportNoticeID != "" {
		return child.ReportNoticeID
	}
	sum := sha256.Sum256([]byte("report\x00" + child.ID + "\x00" + child.HeadEntryID))
	return "notice-" + hex.EncodeToString(sum[:12])
}

// subagentReported posts a child's report to its home session as a notice.
// It is the lifecycle service's Reported option.
func (s *Server) subagentReported(ctx context.Context, child store.Session) {
	s.notifications.send("A subagent reported")
	s.notices.postSoon(ctx, child.ParentSessionID, reportNoticeID(child), reportNotice(child), nil)
}

// endedNoticeID is the entry id of the notice a failed or cancelled
// assignment becomes. The child's head moves on with every assignment, so the
// id names this one ending, and recovery after a restart finds the notice a
// live write already made.
func endedNoticeID(child store.Session) string {
	sum := sha256.Sum256([]byte("ended\x00" + child.ID + "\x00" + child.HeadEntryID + "\x00" + string(child.State)))
	return "notice-" + hex.EncodeToString(sum[:12])
}

// endedNotice is the notice for a child that did not end with a report.
func endedNotice(child store.Session) string {
	text := fmt.Sprintf("%s (%s) %s without a report", child.Name, child.Agent, child.State)
	if child.WaitingReason != "" {
		text += ": " + child.WaitingReason
	}
	return text + ". Use subagent_message to resume it if needed."
}

// subagentEnded posts to a child's home session that its assignment failed or
// was cancelled, with why. It is the lifecycle service's Ended option.
func (s *Server) subagentEnded(ctx context.Context, child store.Session) {
	s.notifications.send("A subagent " + string(child.State))
	s.notices.postSoon(ctx, child.ParentSessionID, endedNoticeID(child), endedNotice(child), nil)
}
