package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

// Bounds on what a probe sends and accepts. A metrics page is the largest
// answer: vLLM's lists every series it has.
const (
	requestTimeout = 5 * time.Second
	maxJSONBody    = 1 << 20
	maxTextBody    = 4 << 20
)

// prober holds the endpoint under probe and reads its routes.
type prober struct {
	// root is the server root: the base URL without the /v1 the OpenAI routes
	// live under. The description routes of every backend are relative to it.
	root   string
	key    string
	models []provider.ModelInfo
	client *http.Client
	// silent is set once a request fails to get any answer at all (a refused
	// connection, a timeout), so an endpoint that is down costs one wait, not
	// one per route.
	silent bool
}

func newProber(t Target) *prober {
	root := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(t.BaseURL), "/"), "/v1")
	return &prober{
		root:   root,
		key:    t.APIKey,
		models: t.Models,
		// No Transport of its own: it shares the default pool, like the
		// provider's client.
		client: &http.Client{Timeout: requestTimeout},
	}
}

// send makes one request and returns the body of a 200 answer, and false for
// any other outcome: a probe asks whether a route exists, so a 404, a refusal
// and a timeout all read as "no".
func (p *prober) send(ctx context.Context, method, path string, body any, limit int64) ([]byte, bool) {
	if p.silent {
		return nil, false
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, false
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.root+path, reader)
	if err != nil {
		return nil, false
	}
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		// Nothing answered, so no later route will: stop asking.
		p.silent = true
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, false
	}
	return raw, true
}

// getJSON reads a JSON document from a route, and reports whether the route
// answered with one.
func (p *prober) getJSON(ctx context.Context, path string, into any) bool {
	raw, ok := p.send(ctx, http.MethodGet, path, nil, maxJSONBody)
	return ok && json.Unmarshal(raw, into) == nil
}

// getText reads a plain-text route, such as a metrics page.
func (p *prober) getText(ctx context.Context, path string) (string, bool) {
	raw, ok := p.send(ctx, http.MethodGet, path, nil, maxTextBody)
	return string(raw), ok
}

// postJSON sends a JSON body to a route and reads the JSON that answers.
func (p *prober) postJSON(ctx context.Context, path string, body, into any) bool {
	raw, ok := p.send(ctx, http.MethodPost, path, body, maxJSONBody)
	return ok && json.Unmarshal(raw, into) == nil
}

// firstModel is the id a request names a model by, for the backends whose
// tokenizer routes ask for one. It is empty when the endpoint lists none.
func (p *prober) firstModel() string {
	if len(p.models) == 0 {
		return ""
	}
	return p.models[0].ID
}

// modelWindow is the smallest positive window the model list states: the most
// one request can use whichever model it names.
func (p *prober) modelWindow() int {
	window := 0
	for _, m := range p.models {
		if m.ContextWindow > 0 && (window == 0 || m.ContextWindow < window) {
			window = m.ContextWindow
		}
	}
	return window
}

// probeMessages is the smallest chat request that proves a route renders one.
var probeMessages = []map[string]string{{"role": "user", "content": "hi"}}
