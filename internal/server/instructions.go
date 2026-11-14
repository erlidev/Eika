package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/store"
)

func decodeInstructionJSON[T any](r *http.Request) (T, error) {
	var zero T
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return zero, tooLargef("the request body is over the limit of %d bytes", maxRequestBytes)
		}
		return zero, invalidf("read request body: %v", err)
	}
	if !utf8.Valid(raw) {
		return zero, invalidf("request body must be valid UTF-8")
	}
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeJSON[T](clone)
}

func (s *Server) handleInstructions(w http.ResponseWriter, r *http.Request) {
	out, err := s.deps.Store.Instructions(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

func (s *Server) handlePutInstructions(w http.ResponseWriter, r *http.Request) {
	body, err := decodeInstructionJSON[*putInstructionsRequest](r)
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
	out, err := s.deps.Store.ProjectInstructions(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, projectInstructionsBody{Instructions: out})
}

func (s *Server) handlePutProjectInstructions(w http.ResponseWriter, r *http.Request) {
	body, err := decodeInstructionJSON[*putProjectInstructionsRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if body == nil || body.Instructions == nil {
		s.fail(w, r, invalidf("instructions must be a string"))
		return
	}
	if err := s.deps.Store.SetProjectInstructions(r.Context(), r.PathValue("id"), *body.Instructions); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, projectInstructionsBody{Instructions: *body.Instructions})
}

func (s *Server) handleDeleteProjectInstructions(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.SetProjectInstructions(r.Context(), r.PathValue("id"), ""); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
