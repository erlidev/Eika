package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/search"
)

// probeTimeout bounds the status report's check that SearXNG answers.
const probeTimeout = 1500 * time.Millisecond

// SearxNG searches a SearXNG instance.
type SearxNG struct {
	client *http.Client
	base   string
}

// NewSearxNG returns a SearxNG on the instance at baseURL, such as
// http://searxng:8080.
func NewSearxNG(client *http.Client, baseURL string) *SearxNG {
	return &SearxNG{client: client, base: strings.TrimRight(baseURL, "/")}
}

// searxngResponse is the part of SearXNG's answer the provider reads.
type searxngResponse struct {
	Results []struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"results"`
	// UnresponsiveEngines lists [engine, reason] pairs. It is decoded
	// leniently, because it only explains an empty answer.
	UnresponsiveEngines json.RawMessage `json:"unresponsive_engines"`
}

// Search runs the query on the instance.
func (s *SearxNG) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	req, err := search.Get(ctx, s.base+"/search?"+url.Values{"q": {q.Text}, "format": {"json"}}.Encode())
	if err != nil {
		return nil, err
	}
	var resp searxngResponse
	if err := search.JSON(ctx, s.client, req, &resp); err != nil {
		var h *search.HTTPError
		if errors.As(err, &h) && h.Status == http.StatusForbidden {
			h.Message = "HTTP 403: SearXNG refused the JSON format; enable json under search.formats in its settings.yml"
		}
		return nil, err
	}
	out := make([]search.Result, 0, min(len(resp.Results), q.Limit))
	for _, r := range resp.Results[:min(len(resp.Results), q.Limit)] {
		out = append(out, search.Result{Title: r.Title, URL: r.URL, Description: search.Clean(r.Content)})
	}
	if len(out) == 0 {
		if reasons := unresponsive(resp.UnresponsiveEngines); reasons != "" {
			return nil, search.Errorf("no engine answered: %s", reasons)
		}
	}
	return out, nil
}

// Probe says whether the instance answers: "up", "HTTP 502", or why it could
// not be reached.
func (s *SearxNG) Probe(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := search.Get(ctx, s.base)
	if err != nil {
		return err.Error()
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return search.DescribeNetwork(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return "up"
}

// unresponsive renders the engines that did not answer, empty when none are
// listed or the list has an unexpected shape.
func unresponsive(raw json.RawMessage) string {
	var pairs [][]string
	if len(raw) == 0 || json.Unmarshal(raw, &pairs) != nil {
		return ""
	}
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		switch len(p) {
		case 0:
		case 1:
			parts = append(parts, p[0])
		default:
			parts = append(parts, fmt.Sprintf("%s (%s)", p[0], p[1]))
		}
	}
	return strings.Join(parts, ", ")
}

var (
	_ search.Searcher = (*SearxNG)(nil)
	_ search.Prober   = (*SearxNG)(nil)
)
