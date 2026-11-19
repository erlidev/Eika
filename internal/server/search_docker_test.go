//go:build docker

package server_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
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

func TestHomeRunSearches(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	p := a.script(
		providertest.Calls("", providertest.Call("c1", "web_search", map[string]any{"query": "tokio"})),
		providertest.Text("done"),
	)
	a.postMessage(t, sess.ID, "find the timeout", "", 202)
	if final := a.waitIdle(t, sess.ID); final.Run == nil || final.Run.State != "done" {
		t.Fatalf("run = %+v", final.Run)
	}
	results := toolResults(p.Requests()[1])
	if len(results) != 1 || !strings.HasPrefix(results[0], "1. Tokio\n   https://tokio.rs/") {
		t.Errorf("tool results = %q", results)
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

func TestResearcherFetchesAWebPage(t *testing.T) {
	d := newDelegating(t)
	eika := providertest.New(call("start", "subagent_start", map[string]any{"agent": "researcher", "name": "reader", "brief": "Read the guide"}), providertest.Text("started"))
	researcher := providertest.New(call("fetch", "web_fetch", map[string]any{"url": "https://docs.example/guide.md"}), call("report", "report", map[string]any{"summary": "Read the guide"}), providertest.Text("done"))
	d.use(byAgent{"You are Eika": answeringNotices(eika), "You are a researcher": researcher})
	d.postMessage(t, d.home.ID, "read the guide", "", 202)
	waitFor(t, "researcher to finish", func() bool {
		child, err := d.store.Subagent(t.Context(), d.home.ID, "reader")
		return err == nil && child.State == store.SessionIdle
	})
	requests := researcher.Requests()
	if len(requests) < 2 {
		t.Fatalf("researcher requests = %d", len(requests))
	}
	results := toolResultsByCall(requests[1])
	if results["fetch"] != "# Guide\n\nthe timeout is 30s\n" {
		t.Fatalf("fetch result = %q", results["fetch"])
	}
	if reqs := d.fetched.Requests(); len(reqs) != 1 || reqs[0].URL.String() != "https://docs.example/guide.md" {
		t.Fatalf("fetch requests = %v", reqs)
	}
}
