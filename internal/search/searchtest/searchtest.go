package searchtest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/erlidev/eika/internal/search"
)

// Searcher answers every query with the same results, or with the same
// error, and records the queries it was sent.
type Searcher struct {
	results []search.Result
	err     error

	mu      sync.Mutex
	queries []search.Query
}

// New returns a searcher that answers with results.
func New(results ...search.Result) *Searcher {
	return &Searcher{results: results}
}

// Failing returns a searcher that answers every query with err.
func Failing(err error) *Searcher {
	return &Searcher{err: err}
}

// Search records the query and answers it with at most q.Limit results.
func (s *Searcher) Search(_ context.Context, q search.Query) ([]search.Result, error) {
	s.mu.Lock()
	s.queries = append(s.queries, q)
	s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return append([]search.Result(nil), s.results[:min(q.Limit, len(s.results))]...), nil
}

// Queries returns the queries the searcher was sent, oldest first.
func (s *Searcher) Queries() []search.Query {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]search.Query(nil), s.queries...)
}

var _ search.Searcher = (*Searcher)(nil)

// Transport answers every request in-process with a handler and records the
// requests, so a backend's test needs no network and can see exactly what
// was sent, bodies included.
type Transport struct {
	handler http.Handler

	mu       sync.Mutex
	requests []Request
}

// Request is one request a Transport answered.
type Request struct {
	*http.Request
	Body []byte
}

// Client returns a client whose every request handler answers.
func Client(handler http.HandlerFunc) (*http.Client, *Transport) {
	t := &Transport{handler: handler}
	return &http.Client{Transport: t}, t
}

// RoundTrip serves req with the handler.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	t.mu.Lock()
	t.requests = append(t.requests, Request{Request: req, Body: body})
	t.mu.Unlock()
	rec := httptest.NewRecorder()
	t.handler.ServeHTTP(rec, req)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

// Requests returns the requests answered, oldest first.
func (t *Transport) Requests() []Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Request(nil), t.requests...)
}

// JSON answers every request with v as JSON.
func JSON(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}
