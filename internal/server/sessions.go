package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// sessionBody is one session on the wire.
type sessionBody struct {
	ID              string    `json:"id"`
	WorkspaceID     string    `json:"workspace_id"`
	Title           string    `json:"title"`
	HeadEntryID     string    `json:"head_entry_id,omitempty"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// entryBody is one session entry on the wire, payload included.
type entryBody struct {
	ID        string           `json:"id"`
	ParentID  string           `json:"parent_id,omitempty"`
	Seq       int64            `json:"seq"`
	Kind      string           `json:"kind"`
	Commit    string           `json:"commit,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	Message   provider.Message `json:"message"`
}

// createSessionRequest is the body of POST /api/sessions.
type createSessionRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
}

// setHeadRequest is the body of POST /api/sessions/{id}/head.
type setHeadRequest struct {
	EntryID string `json:"entry_id"`
}

// forkRequest is the body of POST /api/sessions/{id}/fork.
type forkRequest struct {
	EntryID string `json:"entry_id"`
	Title   string `json:"title"`
}

// sessionsResponse is the body of GET /api/sessions.
type sessionsResponse struct {
	Sessions []sessionBody `json:"sessions"`
}

// sessionResponse is the body of GET /api/sessions/{id}: the session and the
// entry its next run continues from.
type sessionResponse struct {
	Session sessionBody `json:"session"`
	Head    *entryBody  `json:"head,omitempty"`
}

// outlineResponse is the body of GET /api/sessions/{id}/outline: every entry
// of the tree, with the head that says which branch is current.
type outlineResponse struct {
	SessionID   string         `json:"session_id"`
	HeadEntryID string         `json:"head_entry_id,omitempty"`
	Nodes       []session.Node `json:"nodes"`
}

// pathResponse is the body of GET /api/sessions/{id}/path: the branch from
// the root to the head, as the model sees it.
type pathResponse struct {
	SessionID string             `json:"session_id"`
	Entries   []entryBody        `json:"entries"`
	Messages  []provider.Message `json:"messages"`
}

// handleListSessions lists the sessions, of one workspace when the query
// names one.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.deps.Store.Sessions(r.Context(), r.URL.Query().Get("workspace_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]sessionBody, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, asSession(sess))
	}
	writeJSON(w, s.log, http.StatusOK, sessionsResponse{Sessions: out})
}

// handleCreateSession opens a session in a workspace.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createSessionRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		s.fail(w, r, invalidf("title is required"))
		return
	}
	if _, err := s.deps.Store.Workspace(r.Context(), req.WorkspaceID); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.deps.Store.CreateSession(r.Context(), store.Session{
		WorkspaceID: req.WorkspaceID,
		Title:       title,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("session created", "session_id", sess.ID, "workspace_id", sess.WorkspaceID)
	writeJSON(w, s.log, http.StatusCreated, asSession(sess))
}

// handleSession returns a session and the entry it continues from.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.deps.Store.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body := sessionResponse{Session: asSession(sess)}
	if sess.HeadEntryID != "" {
		head, err := s.deps.Store.Entry(r.Context(), sess.HeadEntryID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		entry, err := asEntry(head)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		body.Head = &entry
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// handleSessionOutline returns every entry of the tree without its payloads,
// which is what the session tree panel draws.
func (s *Server) handleSessionOutline(w http.ResponseWriter, r *http.Request) {
	sess, err := s.deps.Store.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	nodes, err := s.tree.Outline(r.Context(), sess.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if nodes == nil {
		nodes = []session.Node{}
	}
	writeJSON(w, s.log, http.StatusOK, outlineResponse{
		SessionID:   sess.ID,
		HeadEntryID: sess.HeadEntryID,
		Nodes:       nodes,
	})
}

// handleSessionPath returns the messages from the session's root to its head.
func (s *Server) handleSessionPath(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.Session(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	entries, err := s.tree.Path(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body := pathResponse{
		SessionID: id,
		Entries:   make([]entryBody, 0, len(entries)),
		Messages:  make([]provider.Message, 0, len(entries)),
	}
	for _, e := range entries {
		entry, err := asEntry(e)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		body.Entries = append(body.Entries, entry)
		if m, ok, err := session.Message(e); err == nil && ok {
			body.Messages = append(body.Messages, m)
		}
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// handleSetSessionHead moves the head to one of the session's entries, which
// branches the tree in place: the next run continues from there.
func (s *Server) handleSetSessionHead(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[setHeadRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	if _, err := s.deps.Store.Session(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.runs.active(id) != nil {
		s.fail(w, r, conflictf("session %s has a run in progress", id))
		return
	}
	if err := s.tree.SetHead(r.Context(), id, req.EntryID); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.deps.Store.Session(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asSession(sess))
}

// handleForkSession copies the branch down to an entry into a session of its
// own, in the same workspace. Forking into a workspace cloned at the entry's
// commit is phase 6 work.
func (s *Server) handleForkSession(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[forkRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(req.EntryID) == "" {
		s.fail(w, r, invalidf("entry_id is required"))
		return
	}
	id := r.PathValue("id")
	if _, err := s.deps.Store.Session(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	fork, err := s.tree.Fork(r.Context(), id, req.EntryID, store.ForkOptions{Title: strings.TrimSpace(req.Title)})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("session forked", "session_id", id, "fork_id", fork.ID, "entry_id", req.EntryID)
	writeJSON(w, s.log, http.StatusCreated, asSession(fork))
}

// handleDeleteSession removes a session and its entries.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.runs.stop(r.Context(), id)
	if err := s.deps.Store.DeleteSession(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("session deleted", "session_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// asSession renders a stored session on the wire.
func asSession(sess store.Session) sessionBody {
	return sessionBody{
		ID:              sess.ID,
		WorkspaceID:     sess.WorkspaceID,
		Title:           sess.Title,
		HeadEntryID:     sess.HeadEntryID,
		ParentSessionID: sess.ParentSessionID,
		CreatedAt:       sess.CreatedAt,
		UpdatedAt:       sess.UpdatedAt,
	}
}

// asEntry renders a stored entry on the wire. An entry that holds something
// other than a message, such as a system note, carries an empty message.
func asEntry(e store.Entry) (entryBody, error) {
	body := entryBody{
		ID:        e.ID,
		ParentID:  e.ParentID,
		Seq:       e.Seq,
		Kind:      string(e.Kind),
		Commit:    e.Commit,
		CreatedAt: e.CreatedAt,
	}
	m, ok, err := session.Message(e)
	if err != nil {
		return entryBody{}, err
	}
	if ok {
		body.Message = m
	}
	return body, nil
}
