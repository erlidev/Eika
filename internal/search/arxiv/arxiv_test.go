package arxiv_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/arxiv"
	"github.com/erlidev/eika/internal/search/searchtest"
)

func TestQuery(t *testing.T) {
	cases := map[string]string{
		"graph neural networks":                    "all:graph AND all:neural AND all:networks",
		`"graph neural networks" robustness`:       `all:"graph neural networks" AND all:robustness`,
		`ti:"graph neural networks" AND cat:cs.LG`: `ti:"graph neural networks" AND cat:cs.LG`,
		"(au:hinton OR au:lecun)":                  "(au:hinton OR au:lecun)",
	}
	for in, want := range cases {
		if got := arxiv.Query(in); got != want {
			t.Errorf("Query(%q) = %q, want %q", in, got, want)
		}
	}
}

const feed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:arxiv="http://arxiv.org/schemas/atom">
  <entry>
    <id>http://arxiv.org/abs/1706.03762v7</id>
    <published>2017-06-12T17:57:34Z</published>
    <title>Attention Is All
      You Need</title>
    <summary>  The dominant sequence transduction models &amp; more.  </summary>
    <author><name>Ashish Vaswani</name></author>
    <author><name>Noam Shazeer</name></author>
    <author><name>Niki Parmar</name></author>
    <author><name>Jakob Uszkoreit</name></author>
    <link href="http://arxiv.org/abs/1706.03762v7" rel="alternate" type="text/html"/>
    <link title="pdf" href="http://arxiv.org/pdf/1706.03762v7" rel="related" type="application/pdf"/>
    <arxiv:primary_category term="cs.CL" scheme="http://arxiv.org/schemas/atom"/>
  </entry>
</feed>`

func atom(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(body))
	}
}

func TestSearchReadsPaperMetadata(t *testing.T) {
	client, tr := searchtest.Client(atom(feed))
	got, err := arxiv.New(client, arxiv.DefaultURL).Search(t.Context(), search.Query{Text: "graph neural networks", Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	want := []search.Result{{
		Title: "Attention Is All You Need",
		URL:   "https://arxiv.org/abs/1706.03762v7",
		Description: "Ashish Vaswani, Noam Shazeer, Niki Parmar, et al. · 2017-06-12 · cs.CL · " +
			"The dominant sequence transduction models & more.",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("results = %+v\nwant %+v", got, want)
	}
	req := tr.Requests()[0]
	q := req.URL.Query()
	if req.URL.Host != "export.arxiv.org" || req.URL.Path != "/api/query" ||
		q.Get("search_query") != "all:graph AND all:neural AND all:networks" ||
		q.Get("max_results") != "25" || q.Get("sortBy") != "relevance" || req.Header.Get("Accept") != "application/atom+xml" {
		t.Errorf("request = %s %v", req.URL, req.Header)
	}
}

func TestSearchSurfacesErrors(t *testing.T) {
	errorFeed := `<feed xmlns="http://www.w3.org/2005/Atom"><entry>
		<id>http://arxiv.org/api/errors#incorrect_id_format_for_1234</id>
		<summary>incorrect id format for 1234</summary></entry></feed>`
	for body, want := range map[string]string{
		errorFeed:                      "arXiv rejected the query: incorrect id format for 1234",
		"<feed><entry>":                "arXiv returned invalid Atom XML",
		strings.Repeat("x", 1_000_001): "arXiv returned an oversized Atom response",
	} {
		client, _ := searchtest.Client(atom(body))
		_, err := arxiv.New(client, arxiv.DefaultURL).Search(t.Context(), search.Query{Text: "q", Limit: 5})
		if err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	}
}
