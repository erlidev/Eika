package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// sessionBody is one session on the wire.
type sessionBody struct {
	ID string `json:"id"`
	// WorkspaceID is the workspace the session's runs act in, absent for a
	// chat.
	WorkspaceID string `json:"workspace_id,omitempty"`
	Title       string `json:"title"`
	// Kind is user, fork, or agent: who opened the session. It is what the
	// session tree draws a row as under the session it came from.
	Kind            string `json:"kind"`
	HeadEntryID     string `json:"head_entry_id,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// Tools names the tools the session's next run offers the model,
	// ordered by name: what the session chose, narrowed to what it can run.
	Tools     []string  `json:"tools"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// createSessionRequest is the body of POST /api/sessions: a session in a
// workspace, or with Chat a session in none.
type createSessionRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Chat        bool   `json:"chat"`
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
	// WithWorkspace gives the fork a workspace of its own, cloned from the
	// hub at the commit the fork entry recorded, so that the files rewind
	// with the conversation.
	WithWorkspace bool `json:"with_workspace"`
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
// names one. `descendants=true` adds the forks and child agents those
// sessions led to, which run in workspaces of their own, so that one request
// returns the whole tree a workspace is the root of. `chats=true` lists the
// chats instead, forks of chats included.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var (
		sessions []store.Session
		err      error
	)
	if query.Get("chats") == "true" {
		if query.Get("workspace_id") != "" {
			s.fail(w, r, invalidf("a chat has no workspace; ask for chats or for a workspace's sessions"))
			return
		}
		sessions, err = s.deps.Store.Chats(r.Context())
	} else {
		sessions, err = s.deps.Store.Sessions(r.Context(),
			query.Get("workspace_id"), query.Get("descendants") == "true")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]sessionBody, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, s.asSession(sess))
	}
	writeJSON(w, s.log, http.StatusOK, sessionsResponse{Sessions: out})
}

// handleCreateSession opens a session in a workspace, or a chat in none. A
// chat is asked for by name rather than by leaving the workspace out, so a
// client that forgets a workspace gets an error, not a chat.
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
	switch {
	case req.Chat && req.WorkspaceID != "":
		s.fail(w, r, invalidf("a chat has no workspace; send chat or workspace_id, not both"))
		return
	case !req.Chat:
		if _, err := s.deps.Store.Workspace(r.Context(), req.WorkspaceID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	sess, err := s.deps.Store.CreateSession(r.Context(), store.Session{
		WorkspaceID: req.WorkspaceID,
		Title:       title,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("session created", "session_id", sess.ID, "workspace_id", sess.WorkspaceID, "chat", sess.Chat())
	writeJSON(w, s.log, http.StatusCreated, s.asSession(sess))
}

// handleSession returns a session and the entry it continues from.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.deps.Store.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body := sessionResponse{Session: s.asSession(sess)}
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
	if req.EntryID != "" {
		if err := s.checkResumable(r.Context(), id, req.EntryID); err != nil {
			s.fail(w, r, err)
			return
		}
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
	writeJSON(w, s.log, http.StatusOK, s.asSession(sess))
}

// handleForkSession copies the branch down to an entry into a session of its
// own. With with_workspace the fork also gets a workspace cloned at the
// commit that entry recorded, so the files rewind with the conversation.
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
	sess, err := s.deps.Store.Session(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.WithWorkspace && sess.Chat() {
		s.fail(w, r, invalidf("a chat has no workspace to fork; fork the conversation alone"))
		return
	}
	// The check comes before the workspace is cloned: a fork that the tree
	// would refuse must not leave a container behind.
	if err := s.checkResumable(r.Context(), id, req.EntryID); err != nil {
		s.fail(w, r, err)
		return
	}
	opts := store.ForkOptions{Title: strings.TrimSpace(req.Title)}
	var forked store.Workspace
	if req.WithWorkspace {
		forked, err = s.forkWorkspace(r.Context(), sess, req.EntryID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		opts.WorkspaceID = forked.ID
	}
	fork, err := s.tree.Fork(r.Context(), id, req.EntryID, opts)
	if err != nil {
		// The workspace was made for a fork that does not exist, so nothing
		// is left to own it.
		if opts.WorkspaceID != "" {
			s.discardRecorded(r.Context(), forked.ID)
		}
		s.fail(w, r, err)
		return
	}
	if opts.WorkspaceID != "" {
		s.workspaceState(r.Context(), forked.ID, forked.ProjectID, forked.State)
	}
	s.log.Info("session forked", "session_id", id, "fork_id", fork.ID, "entry_id", req.EntryID)
	writeJSON(w, s.log, http.StatusCreated, s.asSession(fork))
}

// checkResumable refuses an entry a run cannot continue from. A path that
// stops with tool calls unanswered is a conversation no endpoint accepts, so
// a head or a fork placed there would produce a session that fails on its
// next run rather than one that branches.
func (s *Server) checkResumable(ctx context.Context, sessionID, entryID string) error {
	ok, err := s.tree.Resumable(ctx, sessionID, entryID)
	if err != nil {
		return err
	}
	if !ok {
		return invalidf("entry %s leaves tool calls unanswered, so a run cannot continue from it; "+
			"choose the entry that answers the last of them", entryID)
	}
	return nil
}

// handleDeleteSession removes a session and its entries.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// The run has to stop before the rows it writes to go away, whether or
	// not the client is still waiting for the answer.
	stopCtx, cancel := teardown(r.Context())
	s.runs.stop(stopCtx, id)
	cancel()
	if err := s.deps.Store.DeleteSession(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("session deleted", "session_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// asSession renders a stored session on the wire, with the tools its next
// run offers rather than the stored choice, so a client never re-derives
// which tools a chat may hold.
func (s *Server) asSession(sess store.Session) sessionBody {
	return sessionBody{
		ID:              sess.ID,
		WorkspaceID:     sess.WorkspaceID,
		Title:           sess.Title,
		Kind:            string(sess.Kind),
		HeadEntryID:     sess.HeadEntryID,
		ParentSessionID: sess.ParentSessionID,
		Tools:           s.sessionToolNames(sess),
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
