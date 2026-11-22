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

// Screen scores text and returns its windows, in order, tiling the text.
func (c *Client) Screen(ctx context.Context, text string) ([]Window, error) {
	body, err := json.Marshal(map[string][]string{"texts": {text}})
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
	var out struct {
		Results []struct {
			Windows []Window `json:"windows"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&out); err != nil {
		return nil, fmt.Errorf("read guard response: %w", err)
	}
	if len(out.Results) != 1 {
		return nil, fmt.Errorf("read guard response: %d results for 1 text", len(out.Results))
	}
	return out.Results[0].Windows, nil
}
