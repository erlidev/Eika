package server

import (
	"net/http"

	"github.com/erlidev/eika/internal/agentdef"
)

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	overrides, err := s.deps.Store.AgentOverrides(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := agentsResponse{Agents: make([]agentBody, 0, 3)}
	for _, d := range agentdef.Builtins() {
		o := overrides[d.Name]
		out.Agents = append(out.Agents, agentBody{Definition: o.Apply(d), Builtin: d, Overrides: o})
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	d, ok := agentdef.Lookup(r.PathValue("name"))
	if !ok {
		s.fail(w, r, notFoundf("agent %s does not exist", r.PathValue("name")))
		return
	}
	overrides, err := s.deps.Store.AgentOverrides(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	o := overrides[d.Name]
	writeJSON(w, s.log, http.StatusOK, agentBody{Definition: o.Apply(d), Builtin: d, Overrides: o})
}

func (s *Server) handlePutAgent(w http.ResponseWriter, r *http.Request) {
	if _, ok := agentdef.Lookup(r.PathValue("name")); !ok {
		s.fail(w, r, notFoundf("agent %s does not exist", r.PathValue("name")))
		return
	}
	o, err := decodeJSON[*agentdef.Override](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if o == nil {
		s.fail(w, r, invalidf("agent override must be an object"))
		return
	}
	if err := o.Validate(); err != nil {
		s.fail(w, r, invalidf("%s", err))
		return
	}
	if err := s.deps.Store.SetAgentOverride(r.Context(), r.PathValue("name"), *o); err != nil {
		s.fail(w, r, err)
		return
	}
	s.handleAgent(w, r)
}

func (s *Server) handleResetAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.SetAgentOverride(r.Context(), r.PathValue("name"), agentdef.Override{}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
