//go:build docker

package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// searchStatusWire is the body of GET /api/search/status, as the UI reads it.
type searchStatusWire struct {
	Order    []string `json:"order"`
	Backends []struct {
		Name   string `json:"name"`
		Web    bool   `json:"web"`
		KeySet bool   `json:"key_set"`
		State  string `json:"state"`
		Usage  struct {
			DayUsed int `json:"day_used"`
		} `json:"usage"`
		Limit struct {
			Month int `json:"month"`
		} `json:"limit"`
	} `json:"backends"`
	Keys []struct {
		Name string `json:"name"`
		Set  bool   `json:"set"`
		Hint string `json:"hint"`
	} `json:"keys"`
	CachedSearches int    `json:"cached_searches"`
	SearxNGURL     string `json:"searxng_url"`
}

// searchOutcomeWire is the body of POST /api/search.
type searchOutcomeWire struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error"`
	Details struct {
		Providers []string `json:"providers"`
		Results   []struct {
			URL string `json:"url"`
		} `json:"results"`
	} `json:"details"`
}

func (a *api) searchStatus(t *testing.T) searchStatusWire {
	t.Helper()
	return decodeBody[searchStatusWire](t, request(t, a.Server, "GET", "/api/search/status", nil), 200)
}

func TestSearchStatusReportsEveryBackend(t *testing.T) {
	a := newAPI(t)
	st := a.searchStatus(t)
	if strings.Join(st.Order, ",") != "searxng,exa,tavily,brave,marginalia" || st.SearxNGURL == "" {
		t.Errorf("status = %+v", st)
	}
	names := make([]string, len(st.Backends))
	for i, b := range st.Backends {
		names[i] = b.Name
		if b.Name == "exa" && (b.State != "no API key" || b.Limit.Month != 900) {
			t.Errorf("exa = %+v", b)
		}
	}
	if got := strings.Join(names, ","); got != "searxng,exa,tavily,brave,marginalia,wikipedia,arxiv,github_code,github_repos,github_issues" {
		t.Errorf("backends = %s", got)
	}
	if len(st.Keys) != 4 || st.Keys[0].Name != "exa" || st.Keys[0].Set {
		t.Errorf("keys = %+v", st.Keys)
	}
}

func TestSearchKeysAreSealedAndNeverReturned(t *testing.T) {
	a := newAPI(t)
	const key = "exa-0123456789abcdef"
	rec := request(t, a.Server, "PUT", "/api/search/keys/exa", map[string]any{"key": key})
	if rec.Code != 200 || strings.Contains(rec.Body.String(), key) {
		t.Fatalf("put key = %d: %s", rec.Code, rec.Body.String())
	}
	stored, err := a.store.SearchKey(t.Context(), "exa")
	if err != nil || strings.Contains(string(stored.Key), key) {
		t.Fatalf("stored key = %q, %v; want it sealed", stored.Key, err)
	}

	st := a.searchStatus(t)
	if st.Keys[0].Name != "exa" || !st.Keys[0].Set || st.Keys[0].Hint != "cdef" {
		t.Errorf("keys = %+v", st.Keys)
	}
	for _, b := range st.Backends {
		if b.Name == "exa" && (!b.KeySet || b.State != "ready") {
			t.Errorf("exa = %+v", b)
		}
	}

	if rec := request(t, a.Server, "PUT", "/api/search/keys/exa", map[string]any{"key": " "}); rec.Code != 200 {
		t.Fatalf("clear key = %d", rec.Code)
	}
	if st := a.searchStatus(t); st.Keys[0].Set {
		t.Error("an emptied key is still set")
	}
	if rec := request(t, a.Server, "PUT", "/api/search/keys/openai", map[string]any{"key": key}); rec.Code != 404 {
		t.Errorf("unknown key name = %d", rec.Code)
	}
}

func TestTryingASearchSpendsAndRecordsQuota(t *testing.T) {
	a := newAPI(t)
	out := decodeBody[searchOutcomeWire](t, request(t, a.Server, "POST", "/api/search", map[string]any{"query": "tokio"}), 200)
	if out.IsError || !strings.HasPrefix(out.Text, "1. Tokio") || out.Details.Providers[0] != "searxng" || len(out.Details.Results) != 1 {
		t.Errorf("outcome = %+v", out)
	}
	usage, err := a.store.SearchUsage(t.Context())
	if err != nil || len(usage) != 1 || usage[0].Name != "searxng" || usage[0].DayUsed != 1 {
		t.Errorf("usage = %+v, %v", usage, err)
	}
	empty := decodeBody[searchOutcomeWire](t, request(t, a.Server, "POST", "/api/search", map[string]any{"query": ""}), 200)
	if !empty.IsError {
		t.Error("an empty query succeeded")
	}
	if rec := request(t, a.Server, "POST", "/api/search", map[string]any{"query": "q", "extra": 1}); rec.Code != 400 {
		t.Errorf("unknown field = %d", rec.Code)
	}
}

func TestSearchSettingsAreValidatedAndFollowed(t *testing.T) {
	a := newAPI(t)
	for _, bad := range []map[string]any{
		{"search_order": []string{"marginalia", "google"}},
		{"search_order": []string{"marginalia", "marginalia"}},
		{"search_order": "searxng"},
		{"search_limits": map[string]any{"nope": map[string]int{"day": 1}}},
		{"search_limits": map[string]any{"exa": map[string]int{"month": -1}}},
		{"search_limits": map[string]any{"exa": map[string]int{"day": 10_000_001}}},
		{"search_limits": map[string]any{"exa": map[string]any{"day": 2.5}}},
		{"search_limits": map[string]any{"exa": map[string]any{"day": "abc"}}},
	} {
		if rec := request(t, a.Server, "PUT", "/api/settings", bad); rec.Code != 400 {
			t.Errorf("settings %v = %d, want 400", bad, rec.Code)
		}
	}
	// A quota out of range is named, with the range that works.
	rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"search_limits": map[string]any{"exa": map[string]int{"day": -5}},
	})
	if body := rec.Body.String(); rec.Code != 400 || !strings.Contains(body, "the exa quota must be a whole number from 1 to 10000000") {
		t.Errorf("negative quota = %d %s, want a 400 naming the bucket and the range", rec.Code, body)
	}

	rec = request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"search_order":  []string{"marginalia", "searxng"},
		"search_limits": map[string]any{"marginalia": map[string]int{"day": 1}},
	})
	if rec.Code != 200 {
		t.Fatalf("settings = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Defaults struct {
			SearchOrder  []string                  `json:"search_order"`
			SearchLimits map[string]map[string]int `json:"search_limits"`
		} `json:"defaults"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Defaults.SearchOrder) != 5 || body.Defaults.SearchLimits["tavily"]["month"] != 1000 {
		t.Errorf("defaults = %+v, %v", body.Defaults, err)
	}

	first := decodeBody[searchOutcomeWire](t, request(t, a.Server, "POST", "/api/search", map[string]any{"query": "one"}), 200)
	second := decodeBody[searchOutcomeWire](t, request(t, a.Server, "POST", "/api/search", map[string]any{"query": "two"}), 200)
	if first.Details.Providers[0] != "marginalia" || second.Details.Providers[0] != "searxng" {
		t.Errorf("providers = %v then %v, want marginalia until its quota is spent", first.Details.Providers, second.Details.Providers)
	}
}

func TestARunSearchesAndFetches(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	p := a.script(
		providertest.Calls("", providertest.Call("c1", "web_search", map[string]any{"query": "tokio"})),
		providertest.Calls("", providertest.Call("c2", "web_fetch", map[string]any{"url": "https://docs.example/guide.md"})),
		providertest.Text("done"),
	)
	a.postMessage(t, sess.ID, "find the timeout", "", 202)
	if final := a.waitIdle(t, sess.ID); final.Run == nil || final.Run.State != "done" {
		t.Fatalf("run = %+v", final.Run)
	}
	results := toolResults(p.Requests()[2])
	if len(results) != 2 || !strings.HasPrefix(results[0], "1. Tokio\n   https://tokio.rs/") || results[1] != "# Guide\n\nthe timeout is 30s\n" {
		t.Errorf("tool results = %q", results)
	}
	if reqs := a.fetched.Requests(); len(reqs) != 1 || reqs[0].URL.String() != "https://docs.example/guide.md" {
		t.Errorf("fetched = %v", reqs)
	}
}

// toolResults returns the tool results a request carried, in order.
func toolResults(req provider.Request) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == provider.RoleTool {
			out = append(out, m.Content)
		}
	}
	return out
}
