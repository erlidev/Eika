package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// screenReport is the subagent service's Screen option: a report is cleaned
// before it is stored, so the board, the status tool, the notice and a
// restart's recovery all read the cleaned text. It has no wrapper, since it
// comes from Eika's own subagent.
func (s *Server) screenReport(ctx context.Context, sessionID, text string) (string, error) {
	if s.gate == nil {
		return text, nil
	}
	out, err := s.gate.Ingest(ctx, []sentry.Segment{{
		Source: sentry.Source{Kind: sentry.SourceReport, Session: sessionID, Name: "report"},
		Text:   text,
	}})
	if err != nil {
		return "", err
	}
	return out[0].Text, nil
}

// screenQuestion cleans a subagent's question and options before the notice
// and the question event are made, and returns the question every later step
// uses, so what the user sees and what ValidateAnswer accepts is the cleaned
// text. A question whose whole text is removed is not delivered (ok false):
// there is nothing for Eika to answer, and the asking child is told so and
// carries on. An option that is wholly removed is dropped, and a closed
// question left with no options is not delivered either.
func (s *Server) screenQuestion(ctx context.Context, q builtin.Question) (_ builtin.Question, ok bool, err error) {
	if s.gate == nil {
		return q, true, nil
	}
	src := sentry.Source{Kind: sentry.SourceReport, Session: q.SessionID, Run: q.RunID, Key: q.SessionID + ":" + q.CallID, Name: "question"}
	segs := []sentry.Segment{{Source: src, Text: q.Text}}
	for _, o := range q.Options {
		segs = append(segs, sentry.Segment{Source: src, Text: o})
	}
	got, err := s.gate.Ingest(ctx, segs)
	if err != nil {
		return q, false, err
	}
	q.Text = strings.TrimSpace(got[0].Text)
	if onlyRemoved(q.Text) {
		return q, false, nil
	}
	if len(q.Options) > 0 {
		var options []string
		for _, g := range got[1:] {
			if t := strings.TrimSpace(g.Text); !onlyRemoved(t) {
				options = append(options, t)
			}
		}
		if len(options) == 0 && !q.AllowFreeText {
			return q, false, nil
		}
		q.Options = options
	}
	return q, true, nil
}

// onlyRemoved reports whether nothing is left of text but removal markers.
func onlyRemoved(text string) bool {
	return strings.TrimSpace(strings.ReplaceAll(text, sentry.RemovedMarker, "")) == ""
}

// ingestHistory is the history tools' Screen: child-session text goes through
// the gate like a report, wrapped. Without a gate it passes unchanged.
func (s *Server) ingestHistory(ctx context.Context, segs []sentry.Segment) ([]sentry.Ingested, error) {
	if s.gate == nil {
		out := make([]sentry.Ingested, len(segs))
		for i, seg := range segs {
			out[i] = sentry.Ingested{Text: seg.Text}
		}
		return out, nil
	}
	return s.gate.Ingest(ctx, segs)
}

// guardResponse reports the last observed screening outcome.
type guardResponse struct {
	Status string `json:"status"`
}

func (s *Server) handleGuard(w http.ResponseWriter, _ *http.Request) {
	status := "unknown"
	if g, ok := s.gate.(interface{ GuardHealth() string }); ok {
		status = g.GuardHealth()
	}
	writeJSON(w, s.log, http.StatusOK, guardResponse{Status: status})
}
