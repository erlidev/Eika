package wikipedia

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/erlidev/eika/internal/search"
)

// DefaultURL is English Wikipedia.
const DefaultURL = "https://en.wikipedia.org"

// Source searches one Wikipedia.
type Source struct {
	client *http.Client
	base   string
}

// New returns a Source on the Wikipedia at baseURL.
func New(client *http.Client, baseURL string) *Source {
	return &Source{client: client, base: strings.TrimRight(baseURL, "/")}
}

// Search runs a full-text search.
func (s *Source) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	params := url.Values{
		"action":   {"query"},
		"format":   {"json"},
		"list":     {"search"},
		"srprop":   {"snippet"},
		"srsearch": {q.Text},
		"srlimit":  {strconv.Itoa(q.Limit)},
	}
	req, err := search.Get(ctx, s.base+"/w/api.php?"+params.Encode())
	if err != nil {
		return nil, err
	}
	var body struct {
		Query struct {
			Search []struct {
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := search.JSON(ctx, s.client, req, &body); err != nil {
		return nil, err
	}
	out := make([]search.Result, len(body.Query.Search))
	for i, r := range body.Query.Search {
		out[i] = search.Result{
			Title:       r.Title,
			URL:         s.base + "/wiki/" + url.PathEscape(strings.ReplaceAll(r.Title, " ", "_")),
			Description: search.Clean(r.Snippet),
		}
	}
	return out, nil
}

var _ search.Searcher = (*Source)(nil)
