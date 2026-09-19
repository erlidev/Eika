package web_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/searchtest"
	"github.com/erlidev/eika/internal/search/web"
)

func results(pairs ...string) []search.Result {
	var out []search.Result
	for i := 0; i+2 < len(pairs); i += 3 {
		out = append(out, search.Result{Title: pairs[i], URL: pairs[i+1], Description: pairs[i+2]})
	}
	return out
}

func TestSearxNG(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"results": []map[string]string{
		{"url": "https://tokio.rs/", "title": "Tokio", "content": "An <b>async</b> runtime"},
		{"url": "https://docs.rs/tokio", "title": "tokio - Rust", "content": ""},
	}}))
	got, err := web.NewSearxNG(client, "http://searxng:8080/").Search(t.Context(), search.Query{Text: "tokio", Limit: 30})
	if err != nil {
		t.Fatal(err)
	}
	if want := results("Tokio", "https://tokio.rs/", "An async runtime", "tokio - Rust", "https://docs.rs/tokio", ""); !reflect.DeepEqual(got, want) {
		t.Errorf("results = %+v, want %+v", got, want)
	}
	if u := tr.Requests()[0].URL.String(); u != "http://searxng:8080/search?format=json&q=tokio" {
		t.Errorf("url = %s", u)
	}
}

func TestSearxNGHonoursTheLimit(t *testing.T) {
	many := make([]map[string]string, 40)
	for i := range many {
		many[i] = map[string]string{"url": "https://x/", "title": "x"}
	}
	client, _ := searchtest.Client(searchtest.JSON(map[string]any{"results": many}))
	got, _ := web.NewSearxNG(client, "http://s").Search(t.Context(), search.Query{Text: "q", Limit: 30})
	if len(got) != 30 {
		t.Errorf("got %d results, want 30", len(got))
	}
}

func TestSearxNGExplainsFailures(t *testing.T) {
	client, _ := searchtest.Client(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	_, err := web.NewSearxNG(client, "http://s").Search(t.Context(), search.Query{Text: "q", Limit: 5})
	var h *search.HTTPError
	if !errors.As(err, &h) || h.Status != 403 || !strings.Contains(err.Error(), "settings.yml") {
		t.Errorf("403 error = %v", err)
	}

	client, _ = searchtest.Client(searchtest.JSON(map[string]any{
		"results": []any{}, "unresponsive_engines": [][]string{{"google", "timeout"}},
	}))
	_, err = web.NewSearxNG(client, "http://s").Search(t.Context(), search.Query{Text: "q", Limit: 5})
	if err == nil || err.Error() != "no engine answered: google (timeout)" {
		t.Errorf("empty answer error = %v", err)
	}
}

func TestSearxNGProbe(t *testing.T) {
	up, _ := searchtest.Client(func(http.ResponseWriter, *http.Request) {})
	if got := web.NewSearxNG(up, "http://s").Probe(t.Context()); got != "up" {
		t.Errorf("Probe = %q", got)
	}
	down, _ := searchtest.Client(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) })
	if got := web.NewSearxNG(down, "http://s").Probe(t.Context()); got != "HTTP 502" {
		t.Errorf("Probe = %q", got)
	}
}

func TestTavilySendsItsKeyAndCapsTheCount(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"results": []map[string]string{
		{"title": "T", "url": "https://t/", "content": "c"},
	}}))
	got, err := web.NewTavily(client).Search(t.Context(), search.Query{Text: "q", Limit: 30, Key: "tvly-abc"})
	if err != nil || !reflect.DeepEqual(got, results("T", "https://t/", "c")) {
		t.Fatalf("results = %+v, %v", got, err)
	}
	req := tr.Requests()[0]
	var body map[string]any
	_ = json.Unmarshal(req.Body, &body)
	if req.Header.Get("Authorization") != "Bearer tvly-abc" || body["max_results"] != float64(20) || body["query"] != "q" {
		t.Errorf("request = %v %s", req.Header, req.Body)
	}
}

func TestExaAsksForHighlightsOnly(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"results": []map[string]any{
		{"title": "E", "url": "https://e/", "highlights": []string{"one", "two"}},
		{"url": "https://untitled/"},
	}}))
	got, err := web.NewExa(client).Search(t.Context(), search.Query{Text: "q", Limit: 5, Key: "k"})
	if err != nil || !reflect.DeepEqual(got, results("E", "https://e/", "one two", "https://untitled/", "https://untitled/", "")) {
		t.Fatalf("results = %+v, %v", got, err)
	}
	req := tr.Requests()[0]
	var body struct {
		Contents map[string]any `json:"contents"`
	}
	_ = json.Unmarshal(req.Body, &body)
	if req.Header.Get("X-Api-Key") != "k" || body.Contents["highlights"] == nil || body.Contents["text"] != nil {
		t.Errorf("request = %v %s", req.Header, req.Body)
	}
}

func TestBraveReadsTheNestedResults(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"web": map[string]any{"results": []map[string]string{
		{"title": "B", "url": "https://b/", "description": "<strong>d</strong>"},
	}}}))
	got, err := web.NewBrave(client).Search(t.Context(), search.Query{Text: "q", Limit: 30, Key: "k"})
	if err != nil || !reflect.DeepEqual(got, results("B", "https://b/", "d")) {
		t.Fatalf("results = %+v, %v", got, err)
	}
	req := tr.Requests()[0]
	if req.Header.Get("X-Subscription-Token") != "k" || req.URL.Query().Get("count") != "20" {
		t.Errorf("request = %s %v", req.URL, req.Header)
	}
}

func TestMarginaliaEscapesThePath(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"results": []map[string]string{
		{"url": "https://m/", "title": "M", "description": "small web"},
	}}))
	got, err := web.NewMarginalia(client).Search(t.Context(), search.Query{Text: "rust tokio", Limit: 30})
	if err != nil || !reflect.DeepEqual(got, results("M", "https://m/", "small web")) {
		t.Fatalf("results = %+v, %v", got, err)
	}
	if u := tr.Requests()[0].URL.String(); !strings.HasSuffix(u, "/public/search/rust%20tokio") {
		t.Errorf("url = %s", u)
	}
}

func TestProvidersTolerateAnAnswerWithNoResults(t *testing.T) {
	client, _ := searchtest.Client(searchtest.JSON(map[string]any{}))
	for name, s := range map[string]search.Searcher{
		"searxng": web.NewSearxNG(client, "http://s"), "tavily": web.NewTavily(client), "exa": web.NewExa(client),
		"brave": web.NewBrave(client), "marginalia": web.NewMarginalia(client),
	} {
		if got, err := s.Search(t.Context(), search.Query{Text: "q", Limit: 10, Key: "k"}); err != nil || len(got) != 0 {
			t.Errorf("%s = %+v, %v; want no results", name, got, err)
		}
	}
}

func TestProvidersReportStatusAndHeaders(t *testing.T) {
	client, _ := searchtest.Client(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := web.NewBrave(client).Search(t.Context(), search.Query{Text: "q", Limit: 1, Key: "k"})
	var h *search.HTTPError
	if !errors.As(err, &h) || h.Status != 429 || h.Header.Get("Retry-After") != "60" {
		t.Errorf("err = %#v", err)
	}
}
