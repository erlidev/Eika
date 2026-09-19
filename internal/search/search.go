package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Result is one search hit, the shape every backend reduces its answer to.
type Result struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// Query is one search as a backend receives it.
type Query struct {
	// Text is what to search for, trimmed and bounded by the Engine.
	Text string
	// Limit is the most results the backend should return. It is at least
	// one.
	Limit int
	// Key is the backend's API key, empty when it has none.
	Key string
}

// Searcher answers a query with ranked results. A searcher only speaks its
// backend's wire format: the Engine applies quotas, cooldowns, pacing, and
// caching around it. Implementations are safe for concurrent use.
type Searcher interface {
	// Search returns at most q.Limit results, best first. A failure the
	// backend is to blame for is an *HTTPError, so the Engine can tell a rate
	// limit from an outage.
	Search(ctx context.Context, q Query) ([]Result, error)
}

// Prober is a Searcher that can say whether its service answers, for the
// status report. Only self-hosted backends implement it.
type Prober interface {
	Probe(ctx context.Context) string
}

// UserAgent identifies Eika to the services it queries, which Wikipedia's and
// GitHub's API policies ask of every client.
const UserAgent = "Eika/1.0 (+https://github.com/erlidev/eika)"

// Bounds on one request to a search backend.
const (
	// Timeout bounds one request to a backend.
	Timeout = 12 * time.Second
	// maxResponseBytes bounds the body of a backend's answer, a page of
	// results in JSON or XML.
	maxResponseBytes = 4 << 20
)

// HTTPError is a request that failed in a way the backend is to blame for.
// Status is zero when no response arrived, or when the backend refused the
// query before sending it.
type HTTPError struct {
	Status  int
	Message string
	// Header is the response's header, for the rate-limit deadlines in
	// Retry-After and X-RateLimit-Reset.
	Header http.Header
}

// Error returns the message, which is written for the model.
func (e *HTTPError) Error() string { return e.Message }

// Errorf returns an *HTTPError with no status: a backend refusing a query it
// can tell will fail.
func Errorf(format string, args ...any) error {
	return &HTTPError{Message: fmt.Sprintf(format, args...)}
}

// Describe renders an error as one line a model can act on.
func Describe(err error) string {
	var h *HTTPError
	if !errors.As(err, &h) {
		return DescribeNetwork(err)
	}
	status := strconv.Itoa(h.Status)
	if h.Status > 0 && !strings.Contains(h.Message, status) {
		return h.Message + " - " + status
	}
	return h.Message
}

// DescribeNetwork turns a transport error into a few words: "timed out",
// "connection refused", or "host not found" rather than a wrapped chain of
// dial errors.
func DescribeNetwork(err error) string {
	var dns *net.DNSError
	var ne net.Error
	switch {
	case err == nil:
		return "request failed"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timed out"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &dns) && dns.IsNotFound:
		return "host not found"
	}
	return err.Error()
}

// Do sends req with Eika's user agent under the backend timeout and returns
// at most maxBytes of the body; truncated reports that there was more. A
// status that is not a success is an *HTTPError carrying the response header,
// and so is a transport failure. Cancelling ctx returns ctx's error instead,
// because a cancelled search is nobody's fault.
func Do(ctx context.Context, client *http.Client, req *http.Request, maxBytes int64) (body []byte, truncated bool, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req = req.WithContext(reqCtx)
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, &HTTPError{Message: DescribeNetwork(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, false, &HTTPError{Status: resp.StatusCode, Message: fmt.Sprintf("HTTP %d", resp.StatusCode), Header: resp.Header}
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, &HTTPError{Message: DescribeNetwork(err)}
	}
	if int64(len(body)) > maxBytes {
		return body[:maxBytes], true, nil
	}
	return body, false, nil
}

// JSON sends req and decodes its JSON answer into out.
func JSON(ctx context.Context, client *http.Client, req *http.Request, out any) error {
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	body, truncated, err := Do(ctx, client, req, maxResponseBytes)
	if err != nil {
		return err
	}
	if truncated {
		return &HTTPError{Message: fmt.Sprintf("%s answered with more than %d bytes", req.URL.Host, maxResponseBytes)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &HTTPError{Message: fmt.Sprintf("%s answered with invalid JSON", req.URL.Host)}
	}
	return nil
}

// Get builds a GET request for a backend. The URL is always one the backend
// assembled, so a malformed one is a programming error, not the model's.
func Get(ctx context.Context, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	return req, nil
}

// Post builds a POST request carrying body as JSON.
func Post(ctx context.Context, rawURL string, body any) (*http.Request, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
