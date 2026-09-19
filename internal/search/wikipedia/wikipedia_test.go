package wikipedia_test

import (
	"reflect"
	"testing"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/searchtest"
	"github.com/erlidev/eika/internal/search/wikipedia"
)

func TestSearchUsesTheActionAPI(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"query": map[string]any{"search": []map[string]string{
		{"title": "Tokio (software)", "snippet": `<span class="searchmatch">Tokio</span> is a &quot;runtime&quot;`},
		{"title": "C++/WinRT"},
	}}}))
	got, err := wikipedia.New(client, wikipedia.DefaultURL).Search(t.Context(), search.Query{Text: "tokio rust", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := []search.Result{
		{Title: "Tokio (software)", URL: "https://en.wikipedia.org/wiki/Tokio_%28software%29", Description: `Tokio is a "runtime"`},
		{Title: "C++/WinRT", URL: "https://en.wikipedia.org/wiki/C++%2FWinRT"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("results = %+v\nwant %+v", got, want)
	}
	q := tr.Requests()[0].URL.Query()
	if tr.Requests()[0].URL.Path != "/w/api.php" || q.Get("list") != "search" || q.Get("srsearch") != "tokio rust" || q.Get("srlimit") != "3" {
		t.Errorf("request = %s", tr.Requests()[0].URL)
	}
	if ua := tr.Requests()[0].Header.Get("User-Agent"); ua != search.UserAgent {
		t.Errorf("user agent = %q", ua)
	}
}

func TestSearchToleratesNoResults(t *testing.T) {
	client, _ := searchtest.Client(searchtest.JSON(map[string]any{}))
	if got, err := wikipedia.New(client, wikipedia.DefaultURL).Search(t.Context(), search.Query{Text: "q", Limit: 3}); err != nil || len(got) != 0 {
		t.Errorf("results = %+v, %v", got, err)
	}
}
