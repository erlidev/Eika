package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/sentry"
)

// The Inbox's memory half: the pending list and count, and the user's answers.
// Accepting is an explicit state change of the user (pending to active) with
// no agent-facing equivalent; rejecting archives. Both go through the one
// writer and check content_sha under its lock, so a repeated or stale answer is
// a 409 that writes nothing. Events follow the durable change and carry no
// text; clients refetch.

const (
	maxInboxLimit       = 50
	defaultInboxLimit   = 20
	rejectDefaultReason = "rejected by the user"
)

// publishMemoryChange is the writer's change hook: memory.changed, then the
// pending count, both on the inbox topic. A count that cannot be read is
// logged and skipped; clients refetch on reconnect.
func (s *Server) publishMemoryChange(ctx context.Context, name, state string) {
	if s.deps.Bus == nil {
		return
	}
	if e, err := event.New(event.TypeMemoryChanged, event.TopicInbox, event.MemoryChanged{Name: name, State: state}); err == nil {
		s.deps.Bus.Emit(ctx, e)
	}
	n, err := s.deps.Store.CountMemories(ctx, "pending")
	if err != nil {
		s.log.Warn("count pending memories for event", "error", err)
		return
	}
	if e, err := event.New(event.TypeInboxCount, event.TopicInbox, event.InboxCount{Pending: n}); err == nil {
		s.deps.Bus.Emit(ctx, e)
	}
}

func (s *Server) pendingCount(ctx context.Context) (int, error) {
	return s.deps.Store.CountMemories(ctx, "pending")
}

func (s *Server) handleInboxCount(w http.ResponseWriter, r *http.Request) {
	if _, err := s.memoryWriterFor(); err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := s.pendingCount(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, inboxCountResponse{Pending: n})
}

func (s *Server) handleInboxMemories(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		s.fail(w, r, invalidf("invalid query"))
		return
	}
	limit, after := defaultInboxLimit, ""
	for key, values := range q {
		if len(values) != 1 {
			s.fail(w, r, invalidf("invalid query"))
			return
		}
		switch key {
		case "limit":
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 || n > maxInboxLimit || strconv.Itoa(n) != values[0] {
				s.fail(w, r, invalidf("limit must be an integer from 1 to %d", maxInboxLimit))
				return
			}
			limit = n
		case "after":
			after = values[0]
		default:
			s.fail(w, r, invalidf("unknown query parameter %q", key))
			return
		}
	}
	mw, err := s.memoryWriterFor()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	lq := memory.ListQuery{State: "pending", Limit: limit + 1}
	if after != "" {
		if lq.AfterUpdated, lq.AfterName, err = decodeMemoryCursor(after); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	rows, err := mw.List(r.Context(), lq)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := inboxResponse{Memories: []memoryBody{}}
	if len(rows) > limit {
		rows = rows[:limit]
		out.NextCursor = encodeMemoryCursor(rows[len(rows)-1])
	}
	for _, m := range rows {
		rec, err := mw.Get(r.Context(), m.Name)
		if err != nil {
			if errors.Is(err, memory.ErrNotFound) { // answered while listing
				continue
			}
			s.fail(w, r, memoryError(err))
			return
		}
		if rec.State == "pending" {
			out.Memories = append(out.Memories, memoryDetail(rec))
		}
	}
	if out.Pending, err = s.pendingCount(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// handleAcceptMemory makes a pending memory active, with the user's edits if
// the body carries any. The final text is checked for credentials whatever the
// memory's history: a credential is refused (400) and nothing changes. A text
// the screen still flags is accepted: this is the user's explicit promotion.
// An unavailable required screen is 503.
func (s *Server) handleAcceptMemory(w http.ResponseWriter, r *http.Request) {
	name, err := memoryName(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	req, err := decodeJSON[*editMemoryRequest](r)
	if err != nil || req == nil {
		if err == nil {
			err = invalidf("request body must be an object")
		}
		s.fail(w, r, err)
		return
	}
	if req.ContentSHA == nil || *req.ContentSHA == "" {
		s.fail(w, r, invalidf("content_sha is required"))
		return
	}
	mw, err := s.memoryWriterFor()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cur, err := mw.Get(r.Context(), name)
	if err != nil {
		s.fail(w, r, memoryError(err))
		return
	}
	if cur.ContentSHA != *req.ContentSHA {
		s.fail(w, r, memoryError(memory.ErrStale))
		return
	}
	if cur.State != "pending" {
		s.fail(w, r, memoryError(memory.ErrNotPending))
		return
	}
	edit := memory.Edit{Name: name, IfSHA: *req.ContentSHA, Description: cur.Description, Type: cur.Type, Body: cur.Body, Expires: cur.Expires}
	if req.Description != nil {
		edit.Description = *req.Description
	}
	if req.Type != nil {
		edit.Type = *req.Type
	}
	if req.Body != nil {
		edit.Body = *req.Body
	}
	if req.Expires != nil {
		edit.Expires = *req.Expires
	}
	probe := cur.File
	probe.Description, probe.Type, probe.Body, probe.Expires = edit.Description, edit.Type, edit.Body, edit.Expires
	if err := probe.Validate(name + ".md"); err != nil {
		s.fail(w, r, invalidf("%v", err))
		return
	}
	if err := s.checkAcceptable(r.Context(), name, edit); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := mw.Accept(r.Context(), edit); err != nil {
		s.fail(w, r, memoryError(err))
		return
	}
	s.auditMemory(r.Context(), name, "accept", map[string]any{"edited": edit.Description != cur.Description || edit.Body != cur.Body || edit.Type != cur.Type || edit.Expires != cur.Expires})
	s.answerMemory(w, r, mw, name)
}

// checkAcceptable runs Sentry's memory check on the text about to become
// active. Only a credential (deny) or an unavailable required screen stops it.
func (s *Server) checkAcceptable(ctx context.Context, name string, edit memory.Edit) error {
	checker, ok := s.gate.(sentry.MemoryChecker)
	if !ok {
		return errMemoryBusy("memory cannot be saved right now")
	}
	v, err := checker.CheckMemory(ctx, sentry.MemoryCandidate{Name: name, Description: edit.Description, Body: edit.Body})
	if err != nil {
		return errMemoryBusy("memory cannot be saved right now")
	}
	switch v.Decision {
	case sentry.MemoryActive, sentry.MemoryPending:
		return nil
	case sentry.MemoryDeny:
		reason := v.Reason
		if reason == "" {
			reason = "this memory cannot be saved"
		}
		return invalidf("%s", reason)
	default:
		return errMemoryBusy("memory cannot be saved right now: screening is required and unavailable")
	}
}

// handleRejectMemory archives a pending memory.
func (s *Server) handleRejectMemory(w http.ResponseWriter, r *http.Request) {
	name, err := memoryName(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body, err := decodeJSON[*archiveMemoryRequest](r)
	if err != nil || body == nil {
		if err == nil {
			err = invalidf("request body must be an object")
		}
		s.fail(w, r, err)
		return
	}
	if body.ContentSHA == nil || *body.ContentSHA == "" {
		s.fail(w, r, invalidf("content_sha is required"))
		return
	}
	reason := strings.TrimSpace(shaOf(body.Reason))
	if body.Reason != nil && reason == "" || utf8.RuneCountInString(reason) > maxArchiveReasonRunes || strings.ContainsRune(reason, 0) || !utf8.ValidString(reason) {
		s.fail(w, r, invalidf("reason must be 1 to %d characters", maxArchiveReasonRunes))
		return
	}
	if reason == "" {
		reason = rejectDefaultReason
	}
	mw, err := s.memoryWriterFor()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := mw.Reject(r.Context(), name, reason, *body.ContentSHA); err != nil {
		s.fail(w, r, memoryError(err))
		return
	}
	s.auditMemory(r.Context(), name, "reject", map[string]any{"reason": reason})
	s.answerMemory(w, r, mw, name)
}

func (s *Server) answerMemory(w http.ResponseWriter, r *http.Request, mw *memory.Writer, name string) {
	rec, err := mw.Get(r.Context(), name)
	if err != nil {
		s.fail(w, r, memoryError(err))
		return
	}
	writeJSON(w, s.log, http.StatusOK, memoryDetail(rec))
}
