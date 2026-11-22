// Package screen is the client of the guard service, the injection screen.
// It depends on nothing in Eika. Design: docs/design/sentry.md, section 6.
package screen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Timeout is how long one screen call waits for the guard. A fetched page at
// its cap is about 20 windows of 512 tokens, 2.6 s on a busy CPU at 130 ms a
// window, so 5 s leaves room for a page twice as slow; past it the guard is
// treated as down.
const Timeout = 5 * time.Second

// maxResponse bounds what the client reads back from the guard.
const maxResponse = 8 << 20

// Window is one scored span of the screened text. Start and End are byte
// offsets into it.
type Window struct {
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float64 `json:"score"`
}

// Client calls the guard's POST /screen.
type Client struct {
	url  string
	http *http.Client
}

// New returns a Client for the guard at baseURL. It uses the shared transport.
func New(baseURL string) *Client {
	return &Client{url: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: Timeout}}
}

// MaxTexts is the most texts the guard takes in one call.
const MaxTexts = 64

// Screen scores every text in one call to the guard (more than MaxTexts go in
// further calls) and returns each text's windows, in order, tiling the text.
// No text may be empty.
func (c *Client) Screen(ctx context.Context, texts []string) ([][]Window, error) {
	var out [][]Window
	for len(texts) > 0 {
		n := min(len(texts), MaxTexts)
		part, err := c.call(ctx, texts[:n])
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
		texts = texts[n:]
	}
	return out, nil
}

func (c *Client) call(ctx context.Context, texts []string) ([][]Window, error) {
	body, err := json.Marshal(map[string][]string{"texts": texts})
	if err != nil {
		return nil, fmt.Errorf("encode screen request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/screen", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build screen request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call guard: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("call guard: status %d", resp.StatusCode)
	}
	var body2 struct {
		Results []struct {
			Windows []struct {
				Start *int     `json:"start"`
				End   *int     `json:"end"`
				Score *float64 `json:"score"`
			} `json:"windows"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&body2); err != nil {
		return nil, fmt.Errorf("read guard response: %w", err)
	}
	if len(body2.Results) != len(texts) {
		return nil, fmt.Errorf("read guard response: %d results for %d texts", len(body2.Results), len(texts))
	}
	out := make([][]Window, len(texts))
	for i, r := range body2.Results {
		for _, w := range r.Windows {
			if w.Start == nil || w.End == nil || w.Score == nil {
				return nil, fmt.Errorf("read guard response: incomplete window")
			}
			out[i] = append(out[i], Window{Start: *w.Start, End: *w.End, Score: *w.Score})
		}
	}
	return out, nil
}
