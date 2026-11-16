//go:build docker

package server_test

import (
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

func (a *api) searchStatus(t *testing.T) searchStatusWire {
	t.Helper()
	return decodeBody[searchStatusWire](t, request(t, a.Server, "GET", "/api/search/status", nil), 200)
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
