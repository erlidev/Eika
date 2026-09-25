package server

import (
	"net/http"
	"slices"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

// toolBody is one tool on the wire: what the user turns on or off in a
// session.
type toolBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// NeedsWorkspace reports whether the tool can run only in a session with
	// a workspace, which keeps it out of a chat.
	NeedsWorkspace bool `json:"needs_workspace"`
}

// toolsResponse is the body of GET /api/tools.
type toolsResponse struct {
	Tools []toolBody `json:"tools"`
}

// setToolsRequest is the body of PUT /api/sessions/{id}/tools.
type setToolsRequest struct {
	// Tools names the tools the session's runs may offer the model. It must
	// be present; an empty list is none.
	Tools *[]string `json:"tools"`
}

// handleListTools lists every tool a run can offer the model, ordered by
// name, with whether it needs a workspace.
func (s *Server) handleListTools(w http.ResponseWriter, _ *http.Request) {
	out := toolsResponse{Tools: []toolBody{}}
	if s.deps.Tools != nil {
		for _, t := range s.deps.Tools.List() {
			out.Tools = append(out.Tools, toolBody{
				Name:           t.Name(),
				Description:    t.Description(),
				NeedsWorkspace: tool.NeedsWorkspace(t),
			})
		}
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// handleSetSessionTools chooses the tools a session's next run may offer the
// model. A run already going keeps the tools it started with.
func (s *Server) handleSetSessionTools(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[setToolsRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Tools == nil {
		s.fail(w, r, invalidf("tools is required; an empty list turns every tool off"))
		return
	}
	id := r.PathValue("id")
	sess, err := s.deps.Store.Session(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names := make([]string, 0, len(*req.Tools))
	for _, name := range *req.Tools {
		t, ok := s.toolNamed(name)
		switch {
		case !ok:
			s.fail(w, r, invalidf("there is no tool %q", name))
			return
		case sess.Chat() && tool.NeedsWorkspace(t):
			s.fail(w, r, invalidf("%s needs a workspace, and a chat has none", name))
			return
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if err := s.deps.Store.SetSessionTools(r.Context(), id, names); err != nil {
		s.fail(w, r, err)
		return
	}
	sess.Tools = names
	s.log.Info("session tools set", "session_id", id, "tools", names)
	writeJSON(w, s.log, http.StatusOK, s.asSession(sess))
}

// toolNamed finds a tool in the registry every run shares.
func (s *Server) toolNamed(name string) (tool.Tool, bool) {
	if s.deps.Tools == nil {
		return nil, false
	}
	return s.deps.Tools.Get(name)
}

// sessionTools is the registry a run of sess offers the model: a chat's runs
// never hold a tool that needs a workspace, and a session that chose its
// tools holds those alone. Narrowing the registry, rather than refusing a
// call, means the model is never told about a tool it may not use.
func (s *Server) sessionTools(sess store.Session) *tool.Registry {
	if s.deps.Tools == nil {
		return nil
	}
	return s.deps.Tools.Filter(func(t tool.Tool) bool {
		if sess.Chat() && tool.NeedsWorkspace(t) {
			return false
		}
		return sess.Tools == nil || slices.Contains(sess.Tools, t.Name())
	})
}

// sessionToolNames lists the tools a run of sess offers the model, ordered by
// name.
func (s *Server) sessionToolNames(sess store.Session) []string {
	names := []string{}
	if tools := s.sessionTools(sess); tools != nil {
		for _, t := range tools.List() {
			names = append(names, t.Name())
		}
	}
	return names
}
