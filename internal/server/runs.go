package server

import (
	"net/http"
	"strings"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// The modes a message can be posted in. A run starts a turn; steering and
// follow-up messages join the run that is already going.
const (
	modeRun      = "run"
	modeSteer    = "steer"
	modeFollowUp = "follow_up"
)

// handlePostMessage delivers a message to a session: starting a run, steering
// the run that is going, or queueing a follow-up for after it.
func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[messageRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		s.fail(w, r, invalidf("text is required"))
		return
	}
	id := r.PathValue("id")
	switch mode := req.Mode; mode {
	case "", modeRun:
		run, err := s.runs.start(r.Context(), id, req.Text, req.Model)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, s.log, http.StatusAccepted, asRun(run))
	case modeSteer, modeFollowUp:
		run, err := s.runs.enqueue(r.Context(), id, req.Text, mode)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, s.log, http.StatusAccepted, asRun(run))
	default:
		s.fail(w, r, invalidf("mode must be %q, %q, or %q", modeRun, modeSteer, modeFollowUp))
	}
}

// handleSessionRun reports what a session is doing and what is queued for it.
func (s *Server) handleSessionRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.Session(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	body := runStateResponse{
		SessionID:        id,
		PendingSteering:  []string{},
		PendingFollowUps: []string{},
		Questions:        []builtin.Question{},
		Elicitations:     []mcp.Elicitation{},
	}
	if s.deps.Questions != nil {
		for _, q := range s.deps.Questions.Pending() {
			if q.SessionID == id {
				body.Questions = append(body.Questions, q)
			}
		}
	}
	if s.deps.MCP != nil {
		for _, e := range s.deps.MCP.Elicitations().Pending() {
			if e.SessionID == id {
				body.Elicitations = append(body.Elicitations, e)
			}
		}
	}
	// A run that has only just been claimed has no row and no queues yet, so
	// it reports as active with nothing in it rather than as no run at all.
	if active := s.runs.active(id); active != nil {
		body.Active = true
		if loop := active.loop(); loop != nil {
			body.PendingSteering = append(body.PendingSteering, loop.PendingSteering()...)
			body.PendingFollowUps = append(body.PendingFollowUps, loop.PendingFollowUps()...)
		}
		if runID := active.runID(); runID != "" {
			run, err := s.deps.Store.Run(r.Context(), runID)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			reply := asRun(run)
			body.Run = &reply
		}
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}
	queued := s.runs.pendingFor(id)
	body.PendingSteering = append(body.PendingSteering, queued.steering...)
	body.PendingFollowUps = append(body.PendingFollowUps, queued.followUps...)
	previous, err := s.deps.Store.Runs(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(previous) > 0 {
		reply := asRun(previous[len(previous)-1])
		body.Run = &reply
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// handleAbortRun stops a run and reports how it ended.
func (s *Server) handleAbortRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.runs.abort(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asRun(run))
}
