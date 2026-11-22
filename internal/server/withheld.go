package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
)

// withheldBody is one removal: the text ingest kept out of a session's context.
type withheldBody struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Source    string    `json:"source"`      // web_fetch, web_search, report or plugin
	Name      string    `json:"source_name"` // the URL, query or tool it came from
	SessionID string    `json:"session_id"`
	RunID     string    `json:"run_id"`
	CallKey   string    `json:"call_key"`
	Text      string    `json:"text"`
	Score     float64   `json:"score"`
}

// withheldResponse is a page of withheld rows, newest first.
type withheldResponse struct {
	Withheld   []withheldBody `json:"withheld"`
	NextBefore string         `json:"next_before,omitempty"`
}

func asWithheld(w sentry.Withheld) withheldBody {
	return withheldBody{
		ID: w.ID, At: w.At, Source: string(w.Source.Kind), Name: w.Source.Name,
		SessionID: w.Source.Session, RunID: w.Source.Run, CallKey: w.Source.Key,
		Text: w.Text, Score: w.Score,
	}
}

func withheldPageQuery(r *http.Request) (string, int, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", 0, invalidf("invalid pagination query")
	}
	for key, values := range q {
		if (key != "before" && key != "limit") || len(values) != 1 {
			return "", 0, invalidf("invalid pagination query")
		}
	}
	limit := 50
	if values, ok := q["limit"]; ok {
		n, err := strconv.Atoi(values[0])
		if err != nil || n < 1 || n > 100 || strconv.Itoa(n) != values[0] {
			return "", 0, invalidf("limit must be an integer from 1 to 100")
		}
		limit = n
	}
	before := q.Get("before")
	if _, ok := q["before"]; ok && (before == "" || len(before) > maxEntryCursorBytes || !utf8.ValidString(before) || strings.ContainsRune(before, 0)) {
		return "", 0, invalidf("invalid before cursor")
	}
	return before, limit, nil
}

// handleListWithheld returns withheld rows newest first.
func (s *Server) handleListWithheld(w http.ResponseWriter, r *http.Request) {
	before, limit, err := withheldPageQuery(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if before != "" {
		if _, err = s.deps.Store.Withheld(r.Context(), before); errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, invalidf("invalid before cursor"))
			return
		} else if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	rows, err := s.deps.Store.WithheldBefore(r.Context(), before, limit+1)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body := withheldResponse{Withheld: make([]withheldBody, 0, limit)}
	if len(rows) > limit {
		rows = rows[:limit]
		body.NextBefore = rows[limit-1].ID
	}
	for _, row := range rows {
		body.Withheld = append(body.Withheld, asWithheld(row))
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// handleWithheld returns one row.
func (s *Server) handleWithheld(w http.ResponseWriter, r *http.Request) {
	row, err := s.deps.Store.Withheld(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asWithheld(row))
}
