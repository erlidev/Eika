package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
)

// The Inbox's tripwire half (docs/design/sentry.md, section 11). A tripwire that
// fires becomes one entry, an audit row and one push. The user answers it once:
// "this was fine" ends the hold and makes the destination known, "stop the run"
// aborts the run. An unanswered hold ends with its run, and its entry stays
// open until it is answered.

const maxTripwireEntries = 50

// tripwireEntryBody is one entry. Running says whether its run is still going,
// so a card offers "stop the run" only when it can work.
type tripwireEntryBody struct {
	ID         string     `json:"id"`
	At         time.Time  `json:"at"`
	Tripwire   string     `json:"tripwire"`
	Held       bool       `json:"held"`
	Host       string     `json:"host,omitempty"`
	Source     string     `json:"source,omitempty"`
	Summary    string     `json:"summary"`
	SessionID  string     `json:"session_id"`
	RunID      string     `json:"run_id"`
	Running    bool       `json:"running"`
	Status     string     `json:"status"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
}

type tripwireEntriesResponse struct {
	Entries []tripwireEntryBody `json:"entries"`
}

type tripwireCountBody struct {
	Tripwire string `json:"tripwire"`
	Fired    int    `json:"fired"`
	Fine     int    `json:"fine"`
}

type tripwireCountsResponse struct {
	Counts []tripwireCountBody `json:"counts"`
}

// tripwireWords says in plain words what happened, and where the destination
// came from. It uses what the gate knows when the chain completes, and no text
// from the pages that caused it.
func tripwireWords(tripwire, host, source string) string {
	where := map[string]string{
		"user":    "You typed it.",
		"search":  "A search result named it.",
		"content": "Only something the agent read named it.",
		"model":   "Nothing you or a search gave named it.",
	}[source]
	var what string
	switch tripwire {
	case "followed":
		what = "A page the injection screen found suspect named " + host + ", and the run tried to reach it."
	case "carried_out":
		what = "The run tried to send text it had read from your files or memory to " + host + ", a place it had not reached before."
	case "lifted":
		what = "After reading something sensitive on this computer, the run tried to connect to " + host + ", which only something it read had named."
	case "drained":
		what = "The run sent a large amount of data to " + host + ", a place it had not reached before."
	case "blocked_often":
		return "The run was blocked many times. It may be trying something that is not allowed."
	default:
		what = "A tripwire fired on " + host + "."
	}
	if where == "" || host == "" {
		return what
	}
	return what + " " + where
}

// tripped turns a tripwire that fired into the Inbox entry, its audit row, an
// event and one push. A failure is logged: the hold, if any, stands.
func (s *Server) tripped(ctx context.Context, t sentry.Tripped) {
	ctx = context.WithoutCancel(ctx)
	src := ""
	if t.Host != "" {
		if got, ok, err := s.deps.Store.DestinationSource(ctx, t.Principal.Session, t.Host); err == nil && ok {
			src = string(got)
		} else if err == nil {
			src = string(sentry.DestModel)
		}
	}
	entry, err := s.deps.Store.AddTripwireEntry(ctx, store.TripwireEntry{
		Tripwire: t.Tripwire, Session: t.Principal.Session, Run: t.Principal.Run, Computer: t.Principal.Computer,
		Host: t.Host, Source: src, Held: t.Held, Summary: tripwireWords(t.Tripwire, t.Host, src),
	})
	if err != nil {
		s.log.Error("make tripwire entry", "tripwire", t.Tripwire, "run_id", t.Principal.Run, "error", err)
		return
	}
	s.log.Warn("tripwire fired", "tripwire", t.Tripwire, "held", t.Held, "session_id", t.Principal.Session, "run_id", t.Principal.Run, "host", t.Host)
	s.publishTripwire(ctx, entry)
	if s.notifications != nil {
		message := "Eika: a tripwire fired. " + entry.Summary + " Open the Inbox."
		if t.Held {
			message = "Eika held a run. " + entry.Summary + " Open the Inbox."
		}
		s.notifications.send(message)
	}
}

func (s *Server) publishTripwire(ctx context.Context, e store.TripwireEntry) {
	if s.deps.Bus == nil {
		return
	}
	if ev, err := event.New(event.TypeTripwireChanged, event.TopicInbox, event.TripwireChanged{ID: e.ID, Status: e.Status}); err == nil {
		s.deps.Bus.Emit(ctx, ev)
	}
	s.publishInboxCount(ctx)
}

func (s *Server) tripwireBody(ctx context.Context, e store.TripwireEntry) tripwireEntryBody {
	running := false
	if e.Status == store.TripwireOpen {
		if run, err := s.deps.Store.Run(ctx, e.Run); err == nil {
			running = run.State == store.RunRunning
		}
	}
	return tripwireEntryBody{ID: e.ID, At: e.At, Tripwire: e.Tripwire, Held: e.Held, Host: e.Host, Source: e.Source,
		Summary: e.Summary, SessionID: e.Session, RunID: e.Run, Running: running, Status: e.Status, AnsweredAt: e.AnsweredAt}
}

func (s *Server) handleTripwireEntries(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.OpenTripwireEntries(r.Context(), maxTripwireEntries)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := tripwireEntriesResponse{Entries: make([]tripwireEntryBody, 0, len(rows))}
	for _, e := range rows {
		out.Entries = append(out.Entries, s.tripwireBody(r.Context(), e))
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

func (s *Server) handleTripwireCounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.TripwireCounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := tripwireCountsResponse{Counts: make([]tripwireCountBody, 0, len(rows))}
	for _, c := range rows {
		out.Counts = append(out.Counts, tripwireCountBody{Tripwire: c.Tripwire, Fired: c.Fired, Fine: c.Fine})
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// openTripwire reads the entry an answer is for, refusing one already answered.
func (s *Server) openTripwire(ctx context.Context, id string) (store.TripwireEntry, error) {
	e, err := s.deps.Store.TripwireEntry(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return e, notFoundf("no tripwire entry with that id")
		}
		return e, err
	}
	if e.Status != store.TripwireOpen {
		return e, conflictf("this entry was already answered")
	}
	return e, nil
}

// answerTripwire records the answer once; an entry answered meanwhile is 409.
func (s *Server) answerTripwire(w http.ResponseWriter, r *http.Request, id, status string) {
	e, err := s.deps.Store.AnswerTripwireEntry(r.Context(), id, status)
	if errors.Is(err, store.ErrConflict) {
		err = conflictf("this entry was already answered")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.publishTripwire(r.Context(), e)
	writeJSON(w, s.log, http.StatusOK, s.tripwireBody(r.Context(), e))
}

// handleTripwireFine answers "this was fine": the hold on the run ends and the
// destination becomes known, so the same chain does not fire again.
func (s *Server) handleTripwireFine(w http.ResponseWriter, r *http.Request) {
	e, err := s.openTripwire(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := sentry.Principal{Session: e.Session, Run: e.Run, Computer: e.Computer}
	if err := s.gate.ReleaseHold(r.Context(), p, e.Host); err != nil {
		s.fail(w, r, err)
		return
	}
	s.answerTripwire(w, r, e.ID, store.TripwireFine)
}

// handleTripwireStop answers "stop the run": the run is aborted and the entry
// closed as stopped. A run that already ended (or is gone) has nothing to
// abort, so the entry is closed the same way; no host becomes known.
func (s *Server) handleTripwireStop(w http.ResponseWriter, r *http.Request) {
	e, err := s.openTripwire(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	row, err := s.deps.Store.Run(r.Context(), e.Run)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		s.fail(w, r, err)
		return
	case row.State == store.RunRunning:
		if _, err := s.runs.abort(r.Context(), e.Run); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.answerTripwire(w, r, e.ID, store.TripwireStopped)
}

// markRetention is how long marks are kept, like checkpoints (PLAN.md, phase 4b
// owner question 3). Chains read marks of a run, and the watched set of a
// session; a mark that old can no longer join one.
const markRetention = 30 * 24 * time.Hour

// watchMarkPruning drops old marks at start and once a day until ctx ends.
func (s *Server) watchMarkPruning(ctx context.Context) {
	for {
		n, err := s.deps.Store.PruneMarks(ctx, time.Now().Add(-markRetention))
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			s.log.Warn("prune marks", "error", err)
		case n > 0:
			s.log.Info("pruned marks", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}
