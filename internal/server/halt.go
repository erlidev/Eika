package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/sentry"
)

// maxHaltReason bounds the note a user may leave with a halt.
const maxHaltReason = 200

// haltResponse is the body of GET /api/halt, POST /api/halt and POST
// /api/halt/resume: whether Eika is halted now. At and Reason are set only
// while it is.
type haltResponse struct {
	Halted bool       `json:"halted"`
	At     *time.Time `json:"at,omitempty"`
	Reason string     `json:"reason,omitempty"`
}

// haltRequest is the body of POST /api/halt. Reason is an optional note that
// the audit row and the push carry.
type haltRequest struct {
	Reason string `json:"reason"`
}

func asHalt(st sentry.HaltState) haltResponse {
	out := haltResponse{Halted: st.Halted, Reason: st.Reason}
	if st.Halted && !st.At.IsZero() {
		out.At = &st.At
	}
	return out
}

// halt stops everything: it records the state first, so that a tool call
// already under way is denied and no run starts, then aborts every run, calls
// the scheduler's pause hook, publishes the state and sends one push. Halting
// a halted Eika aborts again, in case a run raced the first halt, and says
// nothing more.
func (s *Server) halt(ctx context.Context, reason string) (sentry.HaltState, error) {
	return s.haltWith(ctx, reason, s.runs.abortAll)
}

// haltWith is halt with the abort given. A halt that a run's own tool call
// causes aborts in the background: abortAll waits for every run to end, and
// that run is waiting for the halt.
func (s *Server) haltWith(ctx context.Context, reason string, abort func()) (sentry.HaltState, error) {
	st, changed, err := s.gate.Halt(ctx, reason)
	if err != nil {
		return st, err
	}
	if s.deps.Egress != nil {
		s.deps.Egress.Halt()
	}
	if changed && s.deps.Pause != nil {
		s.deps.Pause(true)
	}
	abort()
	if changed {
		s.publishHalt(ctx, st)
		msg := "Eika is halted"
		if reason != "" {
			msg += ": " + reason
		}
		s.notifications.send(msg)
		s.log.Warn("eika halted", "reason", reason)
	}
	return st, nil
}

// resume clears the halt.
func (s *Server) resume(ctx context.Context) (sentry.HaltState, error) {
	st, changed, err := s.gate.Resume(ctx)
	if err != nil {
		return st, err
	}
	if changed {
		if s.deps.Pause != nil {
			s.deps.Pause(false)
		}
		s.publishHalt(ctx, st)
		s.log.Info("eika resumed")
	}
	return st, nil
}

func (s *Server) publishHalt(ctx context.Context, st sentry.HaltState) {
	p := event.HaltState{Halted: st.Halted, Reason: st.Reason}
	if st.Halted {
		p.At = &st.At
	}
	if e, err := event.New(event.TypeHaltState, event.TopicGlobal, p); err == nil {
		s.deps.Bus.Emit(ctx, e)
	}
}

// handleHalt returns whether Eika is halted.
func (s *Server) handleHalt(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.log, http.StatusOK, asHalt(s.gate.Halted()))
}

// handleSetHalt halts Eika, and returns once every run has ended.
func (s *Server) handleSetHalt(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[haltRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if len(reason) > maxHaltReason {
		s.fail(w, r, invalidf("a reason may be at most %d bytes", maxHaltReason))
		return
	}
	st, err := s.halt(r.Context(), reason)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asHalt(st))
}

// handleResume lets Eika work again.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	st, err := s.resume(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asHalt(st))
}
