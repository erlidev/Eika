package server

import (
	"net/http"
	"strings"
)

// answerRequest is the body of POST /api/questions/{id}/answer.
type answerRequest struct {
	Answer string `json:"answer"`
}

// handleAnswerQuestion delivers an answer to the run waiting for it. The run
// continues as soon as the answer lands, so the response says nothing beyond
// that it was accepted.
func (s *Server) handleAnswerQuestion(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[answerRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.deps.Questions == nil {
		s.fail(w, r, notFoundf("no run is waiting for an answer"))
		return
	}
	id := r.PathValue("id")
	if err := s.deps.Questions.Answer(id, strings.TrimSpace(req.Answer)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("question answered", "question_id", id)
	w.WriteHeader(http.StatusNoContent)
}
