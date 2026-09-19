package search_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/searchtest"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// clock is a settable time.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fixed serves the same options and keys to every search.
type fixed struct {
	opts search.Options
	keys map[string]string
}

func (f fixed) SearchOptions(context.Context) (search.Options, error) { return f.opts, nil }
func (f fixed) SearchKey(_ context.Context, name string) (string, error) {
	return f.keys[name], nil
}

// chain holds one scripted searcher per backend.
type chain struct {
	searxng, exa, marginalia, wikipedia, arxiv, github *searchtest.Searcher
}

func one(url string) *searchtest.Searcher {
	return searchtest.New(search.Result{Title: url, URL: "https://" + url + "/"})
}

func healthy() chain {
	return chain{
		searxng: one("sx"), exa: one("ex"), marginalia: one("mg"),
		wikipedia: one("wp"), arxiv: one("ax"), github: one("gh"),
	}
}

func newEngine(t *testing.T, c chain, f fixed, clk *clock) *search.Engine {
	t.Helper()
	e, err := search.NewEngine(search.Config{
		Backends: []search.Backend{
			{Name: "searxng", Searcher: c.searxng, Web: true},
			{Name: "exa", Searcher: c.exa, Web: true, Key: "exa", KeyRequired: true, Limit: search.Limit{Month: 900}},
			{Name: "marginalia", Searcher: c.marginalia, Web: true, Limit: search.Limit{Day: 100}},
			{Name: "wikipedia", Searcher: c.wikipedia},
			{Name: "arxiv", Searcher: c.arxiv, Pool: search.MaxCount, Interval: 3 * time.Second},
			{Name: "github_code", Searcher: c.github, Key: "github", KeyRequired: true, Bucket: "github"},
		},
		Settings:   f,
		Keys:       f,
		SearxNGURL: "http://searxng:8080",
		Now:        clk.now,
		Log:        discard(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func start() *clock { return &clock{t: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)} }

func web(t *testing.T, e *search.Engine, query string) search.Outcome {
	t.Helper()
	out, err := e.Search(t.Context(), search.Request{Query: query})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func attempts(d search.Details) string {
	parts := make([]string, len(d.Attempts))
	for i, a := range d.Attempts {
		parts[i] = a.Provider + ":" + a.Error
	}
	return strings.Join(parts, ",")
}

func TestWebUsesTheFirstHealthyProviderOnly(t *testing.T) {
	c := healthy()
	out := web(t, newEngine(t, c, fixed{}, start()), "q")
	if out.IsError || strings.Join(out.Details.Providers, ",") != "searxng" {
		t.Fatalf("outcome = %+v, want searxng to answer", out)
	}
	if len(c.searxng.Queries()) != 1 || len(c.marginalia.Queries()) != 0 {
		t.Error("the search fanned out past the provider that answered")
	}
	if q := c.searxng.Queries()[0]; q.Limit != 30 {
		t.Errorf("searxng was asked for %d results, want the pool of 30", q.Limit)
	}
	if !strings.HasPrefix(out.Text, "1. sx\n   https://sx/") {
		t.Errorf("text = %q", out.Text)
	}
}

func TestWebSkipsProvidersWithoutAKey(t *testing.T) {
	c := healthy()
	e := newEngine(t, c, fixed{opts: search.Options{Order: []string{"exa", "marginalia"}}}, start())
	out := web(t, e, "q")
	if got := attempts(out.Details); got != "exa:no API key" {
		t.Errorf("attempts = %s", got)
	}
	if len(c.exa.Queries()) != 0 || out.Details.Providers[0] != "marginalia" {
		t.Errorf("exa was queried without a key, or marginalia did not answer: %+v", out.Details)
	}

	keyed := newEngine(t, c, fixed{opts: search.Options{Order: []string{"exa"}}, keys: map[string]string{"exa": "k"}}, start())
	web(t, keyed, "q")
	if q := c.exa.Queries(); len(q) != 1 || q[0].Key != "k" {
		t.Errorf("exa queries = %+v, want one carrying its key", q)
	}
}

func TestWebFailsOverAndCoolsTheFailureDown(t *testing.T) {
	clk := start()
	c := healthy()
	c.searxng = searchtest.Failing(&search.HTTPError{Message: "connection refused"})
	e := newEngine(t, c, fixed{}, clk)

	out := web(t, e, "q")
	if got := attempts(out.Details); got != "searxng:connection refused,exa:no API key" {
		t.Errorf("attempts = %s", got)
	}
	if out.Details.Providers[0] != "marginalia" {
		t.Fatalf("providers = %v, want marginalia", out.Details.Providers)
	}
	if !strings.HasPrefix(out.Text, "Notice: SearXNG unavailable (connection refused); used marginalia fallback.") ||
		!strings.Contains(out.Text, "EIKA_SEARXNG_URL") || !strings.Contains(out.Text, "http://searxng:8080") {
		t.Errorf("text does not carry the failover notice:\n%s", out.Text)
	}

	second := web(t, e, "another")
	if got := attempts(second.Details); !strings.HasPrefix(got, "searxng:cooling down 15m") {
		t.Errorf("attempts = %s, want searxng cooling down", got)
	}
	if len(c.searxng.Queries()) != 1 {
		t.Error("a cooling-down provider was queried")
	}
}

func TestWebFailureReasons(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&search.HTTPError{Status: 503, Message: "HTTP 503"}, "searxng:HTTP 503"},
		{&search.HTTPError{Status: 429, Message: "HTTP 429"}, "searxng:rate limited"},
		{search.Errorf("no engine answered: google (timeout)"), "searxng:no engine answered: google (timeout)"},
	} {
		ch := healthy()
		ch.searxng = searchtest.Failing(c.err)
		out := web(t, newEngine(t, ch, fixed{opts: search.Options{Order: []string{"searxng", "marginalia"}}}, start()), "q")
		if got := attempts(out.Details); got != c.want {
			t.Errorf("attempts = %s, want %s", got, c.want)
		}
	}
}

func TestWebHonoursRetryAfter(t *testing.T) {
	clk := start()
	c := healthy()
	c.searxng = searchtest.Failing(&search.HTTPError{Status: 429, Message: "HTTP 429", Header: http.Header{"Retry-After": {"3600"}}})
	e := newEngine(t, c, fixed{}, clk)
	web(t, e, "q")

	clk.advance(59 * time.Minute)
	out := web(t, e, "later")
	if got := attempts(out.Details); !strings.HasPrefix(got, "searxng:cooling down 1m") {
		t.Errorf("attempts = %s, want the server's hour, not the 15m backoff", got)
	}
}

func TestWebMovesOnFromNoResultsAndDedupes(t *testing.T) {
	c := healthy()
	c.searxng = searchtest.New()
	c.marginalia = searchtest.New(
		search.Result{Title: "same", URL: "https://same.com/a"},
		search.Result{Title: "same, trailing slash", URL: "https://same.com/a/"},
	)
	out := web(t, newEngine(t, c, fixed{}, start()), "q")
	if got := attempts(out.Details); got != "searxng:no results,exa:no API key" {
		t.Errorf("attempts = %s", got)
	}
	if out.Details.Pool != 1 || len(out.Details.Results) != 1 {
		t.Errorf("details = %+v, want the duplicate dropped", out.Details)
	}
}

func TestWebServesARepeatFromTheCache(t *testing.T) {
	clk := start()
	c := healthy()
	e := newEngine(t, c, fixed{}, clk)
	web(t, e, "cached query")

	second := web(t, e, "cached query")
	if !second.Details.Cached || len(c.searxng.Queries()) != 1 {
		t.Errorf("repeat = %+v after %d queries, want a cache hit", second.Details, len(c.searxng.Queries()))
	}

	clk.advance(25 * time.Hour)
	if third := web(t, e, "cached query"); third.Details.Cached || len(c.searxng.Queries()) != 2 {
		t.Error("an expired entry was served")
	}
}

func TestWebCachedFallbackKeepsItsNotice(t *testing.T) {
	c := healthy()
	c.searxng = searchtest.Failing(&search.HTTPError{Message: "connection refused"})
	e := newEngine(t, c, fixed{}, start())
	web(t, e, "q")
	out := web(t, e, "q")
	if !out.Details.Cached || !strings.HasPrefix(out.Text, "Notice: Using cached marginalia fallback results") {
		t.Errorf("cached fallback text = %q", out.Text)
	}
	if healthyOut := web(t, newEngine(t, healthy(), fixed{}, start()), "q"); strings.Contains(healthyOut.Text, "Notice") {
		t.Error("a healthy primary produced a notice")
	}
}

func TestWebSkipsASpentQuota(t *testing.T) {
	c := healthy()
	e := newEngine(t, c, fixed{opts: search.Options{
		Order:  []string{"marginalia", "searxng"},
		Limits: map[string]search.Limit{"marginalia": {Day: 1}},
	}}, start())
	web(t, e, "first")
	out := web(t, e, "second")
	if got := attempts(out.Details); got != "marginalia:daily quota spent" || out.Details.Providers[0] != "searxng" {
		t.Errorf("attempts = %s, providers = %v", got, out.Details.Providers)
	}
}

func TestWebReportsEveryFailure(t *testing.T) {
	c := healthy()
	c.searxng = searchtest.Failing(&search.HTTPError{Message: "connection refused"})
	c.marginalia = searchtest.Failing(&search.HTTPError{Message: "timed out"})
	out := web(t, newEngine(t, c, fixed{opts: search.Options{Order: []string{"nope", "searxng", "exa", "marginalia"}}}, start()), "q")
	want := "web search failed: no provider returned results (nope: unknown provider; searxng: connection refused; " +
		"exa: no API key; marginalia: timed out). Set EIKA_SEARXNG_URL"
	if !out.IsError || !strings.HasPrefix(out.Text, want) || !strings.Contains(out.Text, "Settings, Search") {
		t.Errorf("text = %q", out.Text)
	}
}

func TestSearchValidatesTheRequest(t *testing.T) {
	e := newEngine(t, healthy(), fixed{}, start())
	for _, c := range []struct {
		req  search.Request
		want string
	}{
		{search.Request{Query: "  "}, "web search failed: query is empty."},
		{search.Request{Query: strings.Repeat("x", search.MaxQueryRunes+1)}, "web search failed: query is longer than 500 characters."},
		{search.Request{Query: "q", Source: "exa"}, "exa search failed: unknown source; use one of web, wikipedia, arxiv, github_code."},
	} {
		out, err := e.Search(t.Context(), c.req)
		if err != nil || !out.IsError || out.Text != c.want {
			t.Errorf("Search(%+v) = %q, %v; want %q", c.req, out.Text, err, c.want)
		}
	}
}

func TestSearchClampsTheCount(t *testing.T) {
	c := healthy()
	c.wikipedia = searchtest.New(make([]search.Result, 40)...)
	e := newEngine(t, c, fixed{}, start())
	for _, n := range []int{0, -5, 100} {
		out, err := e.Search(t.Context(), search.Request{Query: "q", Source: "wikipedia", Count: n})
		if err != nil {
			t.Fatal(err)
		}
		want := map[int]int{0: 10, -5: 1, 100: 25}[n]
		if out.Details.Count != want || c.wikipedia.Queries()[len(c.wikipedia.Queries())-1].Limit != want {
			t.Errorf("count %d became %d, want %d", n, out.Details.Count, want)
		}
	}
}

func TestSourceSearchGoesStraightToItsBackend(t *testing.T) {
	c := healthy()
	out, err := newEngine(t, c, fixed{}, start()).Search(t.Context(), search.Request{Query: "tokio rust", Source: "wikipedia", Count: 3})
	if err != nil || out.IsError || out.Text != "1. wp\n   https://wp/" {
		t.Fatalf("Search = %q, %v", out.Text, err)
	}
	if len(c.searxng.Queries()) != 0 {
		t.Error("a source search went through the web chain")
	}
}

func TestSourceNeedsItsKey(t *testing.T) {
	c := healthy()
	out, _ := newEngine(t, c, fixed{}, start()).Search(t.Context(), search.Request{Query: "q", Source: "github_code"})
	if !out.IsError || out.Text != "github_code search failed: github_code needs a github key; add one under Settings, Search." {
		t.Errorf("text = %q", out.Text)
	}
	if len(c.github.Queries()) != 0 {
		t.Error("github was queried without its key")
	}
}

func TestSourceRateLimitCoolsItsBucketDown(t *testing.T) {
	clk := start()
	c := healthy()
	reset := clk.now().Add(2 * time.Hour).Unix()
	c.github = searchtest.Failing(&search.HTTPError{
		Status: 403, Message: "HTTP 403",
		Header: http.Header{"X-Ratelimit-Reset": {strconv.FormatInt(reset, 10)}},
	})
	e := newEngine(t, c, fixed{keys: map[string]string{"github": "t"}}, clk)
	req := search.Request{Query: "q", Source: "github_code"}

	first, _ := e.Search(t.Context(), req)
	if first.Text != "github_code search failed: HTTP 403" {
		t.Errorf("first = %q", first.Text)
	}
	second, _ := e.Search(t.Context(), req)
	if second.Text != "github_code search failed: github cooling down 120m." || len(c.github.Queries()) != 1 {
		t.Errorf("second = %q after %d queries", second.Text, len(c.github.Queries()))
	}
}

func TestSourceOtherFailuresDoNotCoolDown(t *testing.T) {
	c := healthy()
	c.github = searchtest.Failing(&search.HTTPError{Status: 422, Message: "HTTP 422"})
	e := newEngine(t, c, fixed{keys: map[string]string{"github": "t"}}, start())
	for range 2 {
		e.Search(t.Context(), search.Request{Query: "q", Source: "github_code"})
	}
	if len(c.github.Queries()) != 2 {
		t.Error("a rejected query cooled GitHub down")
	}
}

func TestPooledSourceCachesAndPaces(t *testing.T) {
	clk := start()
	c := healthy()
	e := newEngine(t, c, fixed{}, clk)
	ask := func(q string, count int) search.Outcome {
		out, err := e.Search(t.Context(), search.Request{Query: q, Source: "arxiv", Count: count})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	ask("attention", 2)
	if q := c.arxiv.Queries(); len(q) != 1 || q[0].Limit != search.MaxCount {
		t.Fatalf("queries = %+v, want one for the whole pool", q)
	}
	if out := ask("attention", 20); !out.Details.Cached || len(c.arxiv.Queries()) != 1 {
		t.Error("a larger count for the same query was not served from the pool")
	}

	// The clock does not move, so the next request must wait out the
	// interval. A cancelled context proves it waited instead of sending.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := e.Search(ctx, search.Request{Query: "other", Source: "arxiv"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Search inside the interval = %v, want it to wait until cancelled", err)
	}
	clk.advance(3 * time.Second)
	if out := ask("other", 1); out.IsError || len(c.arxiv.Queries()) != 2 {
		t.Errorf("a request after the interval = %+v", out)
	}
}

func TestStatus(t *testing.T) {
	clk := start()
	c := healthy()
	c.searxng = searchtest.Failing(&search.HTTPError{Message: "connection refused"})
	e := newEngine(t, c, fixed{keys: map[string]string{"github": "t"}}, clk)
	web(t, e, "q")

	st, err := e.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]search.BackendStatus{}
	for _, b := range st.Backends {
		states[b.Name] = b
	}
	if s := states["searxng"]; s.State != "cooling down 15m" {
		t.Errorf("searxng = %+v", s)
	}
	if s := states["exa"]; s.State != "no API key" || s.KeySet || s.Limit.Month != 900 {
		t.Errorf("exa = %+v", s)
	}
	if s := states["marginalia"]; s.State != "ready" || s.Usage.DayUsed != 1 || s.Limit.Day != 100 {
		t.Errorf("marginalia = %+v", s)
	}
	if s := states["github_code"]; !s.KeySet || s.State != "ready" {
		t.Errorf("github_code = %+v", s)
	}
	if strings.Join(st.Order, ",") != "searxng,exa,marginalia" || st.CachedSearches != 1 {
		t.Errorf("status = %+v", st)
	}
}

func TestNewEngineRejectsBadBackends(t *testing.T) {
	s := searchtest.New()
	for _, backends := range [][]search.Backend{
		{{Name: "web", Searcher: s}},
		{{Name: "", Searcher: s}},
		{{Name: "a", Searcher: s}, {Name: "a", Searcher: s}},
		{{Name: "a"}},
	} {
		if _, err := search.NewEngine(search.Config{Backends: backends}); err == nil {
			t.Errorf("NewEngine(%+v) succeeded", backends)
		}
	}
}

func TestWebIsOffWithAnEmptyOrder(t *testing.T) {
	c := healthy()
	e := newEngine(t, c, fixed{opts: search.Options{Order: []string{}}}, start())
	out := web(t, e, "q")
	if !out.IsError || !strings.Contains(out.Text, "turned off") {
		t.Fatalf("outcome = %+v, want web search off", out)
	}
	if len(c.searxng.Queries()) != 0 || len(c.marginalia.Queries()) != 0 {
		t.Error("a provider was queried with none selected")
	}
	st, err := e.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Order) != 0 {
		t.Errorf("status order = %v, want none", st.Order)
	}
}
