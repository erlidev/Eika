package server

import (
	"net/http"

	"github.com/erlidev/eika/internal/store"
)

func (s *Server) handleInstructions(w http.ResponseWriter, r *http.Request) {
	out, err := s.deps.Store.Instructions(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

func (s *Server) handlePutInstructions(w http.ResponseWriter, r *http.Request) {
	body, err := decodeJSON[*putInstructionsRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if body == nil || body.AboutMe == nil || body.Rules == nil {
		s.fail(w, r, invalidf("about_me and rules must be strings"))
		return
	}
	out := store.GlobalInstructions{AboutMe: *body.AboutMe, Rules: *body.Rules}
	if err := s.deps.Store.SetInstructions(r.Context(), out); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

func (s *Server) handleDeleteInstructions(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.SetInstructions(r.Context(), store.GlobalInstructions{}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProjectInstructions(w http.ResponseWriter, r *http.Request) {
	out, version, err := s.deps.Store.ProjectInstructionVersion(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, projectInstructionsBody{Instructions: out, Version: version})
}

func (s *Server) handlePutProjectInstructions(w http.ResponseWriter, r *http.Request) {
	body, err := decodeJSON[*putProjectInstructionsRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if body == nil || body.Instructions == nil || body.Version == nil {
		s.fail(w, r, invalidf("instructions must be a string"))
		return
	}
	version, err := s.deps.Store.ReplaceProjectInstructions(r.Context(), r.PathValue("id"), *body.Instructions, body.Version)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, projectInstructionsBody{Instructions: *body.Instructions, Version: version})
}

func (s *Server) handleDeleteProjectInstructions(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.SetProjectInstructions(r.Context(), r.PathValue("id"), ""); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
