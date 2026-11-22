package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
)

// The user's memory API: list, search, read and the changes only the user
// makes. Every change goes through the memory writer, which serialises file
// write, commit and index row, so the API has no write path of its own. The
// stale-edit precondition is checked again inside the writer's lock.

const (
	maxArchiveReasonRunes = 200
	// pinCountSession names the token counter that sizes the pinned section
	// for a pin or an edit of a pinned memory. It is not a session row.
	pinCountSession = "memory-pins"
)

// errMemoryBusy is a memory change that could not be judged right now: no
// writer yet, no screening, or no exact token count. Nothing was written.
func errMemoryBusy(why string) error {
	return apiError{status: http.StatusServiceUnavailable, code: codeInternal, message: why}
}

// memoryError maps what the writer returns onto the API's errors.
func memoryError(err error) error {
	switch {
	case errors.Is(err, memory.ErrNotFound):
		return notFoundf("no memory with that name")
	case errors.Is(err, memory.ErrStale):
		return conflictf("this memory changed since it was read; reload it and try again")
	case errors.Is(err, memory.ErrNotActive):
		return conflictf("only an active memory can be pinned; accept a pending one first")
	case errors.Is(err, memory.ErrNotPending):
		return conflictf("this memory is not pending; it was already answered")
	case errors.Is(err, memory.ErrTooManyPins), errors.Is(err, memory.ErrPinsOverBudget):
		return conflictf("%s; unpin another memory first", err.Error())
	case errors.Is(err, memory.ErrNotExact):
		return errMemoryBusy("pins cannot be checked right now: the model's token count is not exact")
	}
	return err
}

func (s *Server) memoryWriterFor() (*memory.Writer, error) {
	if w := s.MemoryWriter(); w != nil {
		return w, nil
	}
	return nil, errMemoryBusy(errNoMemory.Error())
}

// memoryName reads and checks the {name} of a route. An invalid name cannot
// exist, so it is a not found.
func memoryName(r *http.Request) (string, error) {
	name := r.PathValue("name")
	if !memory.ValidName(name) {
		return "", notFoundf("no memory with that name")
	}
	return name, nil
}

func memoryDetail(rec memory.Record) memoryBody {
	return memoryBody{
		Name: rec.Name, Description: rec.Description, Type: rec.Type, Source: rec.Source,
		Created: rec.Created, Updated: rec.Updated, Expires: rec.Expires, Pinned: rec.Pinned,
		State: rec.State, Body: rec.Body, ContentSHA: rec.ContentSHA, Screen: rec.Screen, ArchiveReason: rec.Reason,
	}
}

// memoryQuery is the parsed query of GET /api/memories.
type memoryQuery struct {
	text, typ, state string
	pinned           *bool
	includeArchived  bool
	limit            int
	after            string
}

func memoryListQuery(r *http.Request) (memoryQuery, error) {
	var mq memoryQuery
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return mq, invalidf("invalid query")
	}
	for key, values := range q {
		if len(values) != 1 {
			return mq, invalidf("invalid query")
		}
		v := values[0]
		if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return mq, invalidf("invalid query")
		}
		switch key {
		case "q":
			mq.text = strings.TrimSpace(v)
			if mq.text == "" || utf8.RuneCountInString(mq.text) > memory.MaxQueryRunes {
				return mq, invalidf("q must be 1 to %d characters", memory.MaxQueryRunes)
			}
		case "type":
			switch v {
			case "user", "preference", "person", "fact", "procedure":
				mq.typ = v
			default:
				return mq, invalidf("type must be user, preference, person, fact or procedure")
			}
		case "state":
			switch v {
			case "active", "pending", "archived":
				mq.state = v
			default:
				return mq, invalidf("state must be active, pending or archived")
			}
		case "pinned", "include_archived":
			b, err := strconv.ParseBool(v)
			if err != nil || (v != "true" && v != "false") {
				return mq, invalidf("%s must be true or false", key)
			}
			if key == "pinned" {
				mq.pinned = &b
			} else {
				mq.includeArchived = b
			}
		case "limit":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > memory.MaxLimit || strconv.Itoa(n) != v {
				return mq, invalidf("limit must be an integer from 1 to %d", memory.MaxLimit)
			}
			mq.limit = n
		case "after":
			mq.after = v
		default:
			return mq, invalidf("unknown query parameter %q", key)
		}
	}
	if mq.limit == 0 {
		mq.limit = 20
	}
	if mq.text != "" && (mq.after != "" || mq.state != "" || mq.pinned != nil) {
		return mq, invalidf("a search takes only q, type, include_archived and limit")
	}
	if mq.text == "" && mq.includeArchived {
		return mq, invalidf("include_archived applies to a search; use state to list archived memories")
	}
	return mq, nil
}

// memoryCursor is the position after the last row of a page.
type memoryCursor struct {
	Updated string `json:"u"`
	Name    string `json:"n"`
}

func encodeMemoryCursor(m memory.Summary) string {
	b, _ := json.Marshal(memoryCursor{Updated: m.Updated, Name: m.Name})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeMemoryCursor(s string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	var c memoryCursor
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	var at time.Time
	if err == nil {
		at, err = time.Parse(time.RFC3339, c.Updated)
	}
	if err != nil || !memory.ValidName(c.Name) {
		return time.Time{}, "", invalidf("invalid after cursor")
	}
	return at, c.Name, nil
}

func (s *Server) handleListMemories(w http.ResponseWriter, r *http.Request) {
	mq, err := memoryListQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	mw, err := s.memoryWriterFor()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := memoriesResponse{Memories: []memoryItem{}}
	if mq.text != "" {
		hits, err := mw.Search(r.Context(), memory.Query{Text: mq.text, Type: mq.typ, IncludeArchived: mq.includeArchived, Limit: mq.limit})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, h := range hits {
			out.Memories = append(out.Memories, memoryItem{Name: h.Name, Description: h.Description, Type: h.Type, State: h.State, Pinned: h.Pinned, Snippet: h.Snippet})
		}
		writeJSON(w, s.log, http.StatusOK, out)
		return
	}
	lq := memory.ListQuery{State: mq.state, Type: mq.typ, Pinned: mq.pinned, Limit: mq.limit + 1}
	if mq.after != "" {
		if lq.AfterUpdated, lq.AfterName, err = decodeMemoryCursor(mq.after); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	rows, err := mw.List(r.Context(), lq)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(rows) > mq.limit {
		rows = rows[:mq.limit]
		out.NextCursor = encodeMemoryCursor(rows[len(rows)-1])
	}
	for _, m := range rows {
		out.Memories = append(out.Memories, memoryItem{Name: m.Name, Description: m.Description, Type: m.Type, State: m.State,
			Pinned: m.Pinned, Expires: m.Expires, Updated: m.Updated, ContentSHA: m.ContentSHA})
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	name, err := memoryName(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	mw, err := s.memoryWriterFor()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rec, err := mw.Get(r.Context(), name)
	if err != nil {
		s.fail(w, r, memoryError(err))
		return
	}
	writeJSON(w, s.log, http.StatusOK, memoryDetail(rec))
}

// auditMemory records a user's change in Sentry's audit log, as settings are.
// The change is already made, so a failure to record it is logged.
func (s *Server) auditMemory(ctx context.Context, name, action string, detail map[string]any) {
	b, _ := json.Marshal(detail)
	err := s.deps.Store.AppendAudit(ctx, sentry.AuditEntry{Event: "setting", Kind: "memory", Name: name, Reason: action, Detail: b})
	if err != nil {
		s.log.Error("audit memory change", "memory", name, "action", action, "error", err)
	}
}

// pinCounter returns the token counter for the pinned section and the
// function that ends its use of the model's routes, which the caller calls
// when the request is done. It is built on first count, so a change that never
// needs one never needs a model. A counter that can only estimate answers not
// exact, and the change is refused.
func (s *Server) pinCounter() (memory.CountFunc, func()) {
	return s.exactCounter(pinCountSession)
}

// exactCounter is the lazily built exact token counter behind pinCounter, for
// any change that must be judged against a budget; sessionID names the counter
// and is not a session row.
func (s *Server) exactCounter(sessionID string) (func(context.Context, string) (int, bool, error), func()) {
	var mu sync.Mutex
	var count func(context.Context, string) (int, bool)
	var release func()
	built := false
	fn := func(ctx context.Context, text string) (int, bool, error) {
		mu.Lock()
		if !built {
			built = true
			cfg, err := s.configure(ctx, store.Session{ID: sessionID, Agent: "eika"}, "")
			if err != nil {
				s.log.Warn("count pinned memories: no model", "error", err)
			} else {
				ctr, rel := s.counterFor(ctx, sessionID, cfg.model, cfg.modelOK)
				count, release = ctr.CountText, rel
			}
		}
		c := count
		mu.Unlock()
		if c == nil {
			return 0, false, nil
		}
		ctx, cancel := context.WithTimeout(ctx, agent.DefaultCountTimeout)
		defer cancel()
		n, exact := c(ctx, text)
		return n, exact, nil
	}
	return fn, func() {
		mu.Lock()
		defer mu.Unlock()
		if release != nil {
			release()
		}
	}
}

// mutateMemory runs a writer change that needs no screen, then answers with
// the memory as stored.
func (s *Server) mutateMemory(w http.ResponseWriter, r *http.Request, action string, do func(mw *memory.Writer, name string) error, detail map[string]any) {
	name, err := memoryName(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	mw, err := s.memoryWriterFor()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := do(mw, name); err != nil {
		s.fail(w, r, memoryError(err))
		return
	}
	s.auditMemory(r.Context(), name, action, detail)
	if action == "purge" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	rec, err := mw.Get(r.Context(), name)
	if err != nil {
		s.fail(w, r, memoryError(err))
		return
	}
	writeJSON(w, s.log, http.StatusOK, memoryDetail(rec))
}

func shaOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) handlePinMemory(pin bool) http.HandlerFunc {
	action := "unpin"
	if pin {
		action = "pin"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeOptionalJSON[memoryPreconditionRequest](r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.mutateMemory(w, r, action, func(mw *memory.Writer, name string) error {
			count, release := s.pinCounter()
			defer release()
			_, err := mw.SetPinned(r.Context(), name, pin, shaOf(body.ContentSHA), count)
			return err
		}, nil)
	}
}

func (s *Server) handleArchiveMemory(w http.ResponseWriter, r *http.Request) {
	body, err := decodeOptionalJSON[archiveMemoryRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reason := strings.TrimSpace(shaOf(body.Reason))
	if body.Reason != nil && reason == "" || utf8.RuneCountInString(reason) > maxArchiveReasonRunes || strings.ContainsRune(reason, 0) || !utf8.ValidString(reason) {
		s.fail(w, r, invalidf("reason must be 1 to %d characters", maxArchiveReasonRunes))
		return
	}
	if reason == "" {
		reason = "archived by the user"
	}
	s.mutateMemory(w, r, "archive", func(mw *memory.Writer, name string) error {
		_, err := mw.ArchiveIf(r.Context(), name, reason, shaOf(body.ContentSHA))
		return err
	}, map[string]any{"reason": reason})
}

// handlePurgeMemory removes the file and index row in a commit. Git history
// keeps the text; so can old sessions and backups.
func (s *Server) handlePurgeMemory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for key := range q {
		if key != "content_sha" {
			s.fail(w, r, invalidf("unknown query parameter %q", key))
			return
		}
	}
	sha := q.Get("content_sha")
	s.mutateMemory(w, r, "purge", func(mw *memory.Writer, name string) error {
		return mw.Purge(r.Context(), name, sha)
	}, nil)
}

// handleEditMemory applies a user's edit. The edit is screened like an
// agent's write: a credential is refused, a flagged edit leaves the memory
// pending for the user to accept in the Inbox, and only a clean edit of an
// active memory keeps it active.
func (s *Server) handleEditMemory(w http.ResponseWriter, r *http.Request) {
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
	if req.Description == nil && req.Type == nil && req.Body == nil && req.Expires == nil {
		s.fail(w, r, invalidf("nothing to change"))
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
	edit := memory.Edit{Name: name, IfSHA: *req.ContentSHA, Description: cur.Description, Type: cur.Type, Body: cur.Body, Expires: cur.Expires}
	count, release := s.pinCounter()
	defer release()
	edit.Count = count
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
	// Format and bounds first, so a refusal names the field and nothing is
	// audited or screened for an edit that could never be stored.
	probe := cur.File
	probe.Description, probe.Type, probe.Body, probe.Expires = edit.Description, edit.Type, edit.Body, edit.Expires
	if err := probe.Validate(name + ".md"); err != nil {
		s.fail(w, r, invalidf("%v", err))
		return
	}
	textChanged := edit.Description != cur.Description || edit.Body != cur.Body
	if textChanged {
		if err := s.screenUserEdit(r.Context(), name, &edit, cur); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if _, err := mw.Edit(r.Context(), edit); err != nil {
		s.fail(w, r, memoryError(err))
		return
	}
	s.auditMemory(r.Context(), name, "edit", map[string]any{"state": edit.State})
	rec, err := mw.Get(r.Context(), name)
	if err != nil {
		s.fail(w, r, memoryError(err))
		return
	}
	writeJSON(w, s.log, http.StatusOK, memoryDetail(rec))
}

// screenUserEdit runs Sentry's memory check on an edit's text and sets the
// edit's state and screen from the verdict. A credential is refused with its
// reason; a check that cannot run writes nothing.
func (s *Server) screenUserEdit(ctx context.Context, name string, edit *memory.Edit, cur memory.Record) error {
	checker, ok := s.gate.(sentry.MemoryChecker)
	if !ok {
		return errMemoryBusy("memory cannot be saved right now")
	}
	v, err := checker.CheckMemory(ctx, sentry.MemoryCandidate{Name: name, Description: edit.Description, Body: edit.Body})
	if err != nil {
		return errMemoryBusy("memory cannot be saved right now")
	}
	switch v.Decision {
	case sentry.MemoryActive:
		if cur.State == "pending" {
			edit.Screen = &memory.ScreenResult{Spans: []memory.ScreenSpan{}}
		}
	case sentry.MemoryPending:
		res := &memory.ScreenResult{MaxScore: v.MaxScore, Unscanned: v.Unscanned, Spans: []memory.ScreenSpan{}}
		for _, sp := range v.Spans {
			res.Spans = append(res.Spans, memory.ScreenSpan(sp))
		}
		edit.Screen = res
		if cur.State == "active" {
			edit.State = "pending"
		}
	case sentry.MemoryDeny:
		reason := v.Reason
		if reason == "" {
			reason = "this memory cannot be saved"
		}
		return invalidf("%s", reason)
	default: // reject, and anything unknown
		return errMemoryBusy("memory cannot be saved right now: screening is required and unavailable")
	}
	return nil
}

// decodeOptionalJSON is decodeJSON for a body that may be empty.
func decodeOptionalJSON[T any](r *http.Request) (T, error) {
	var zero T
	if r.ContentLength == 0 {
		return zero, nil
	}
	v, err := decodeJSON[*T](r)
	if err != nil {
		return zero, err
	}
	if v == nil {
		return zero, nil
	}
	return *v, nil
}
