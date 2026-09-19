package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/erlidev/eika/internal/search"
)

// Tavily searches the Tavily API. It needs a key.
type Tavily struct{ client *http.Client }

// NewTavily returns a Tavily provider.
func NewTavily(client *http.Client) *Tavily { return &Tavily{client: client} }

// Search runs the query on Tavily.
func (t *Tavily) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	req, err := search.Post(ctx, "https://api.tavily.com/search", map[string]any{
		"query": q.Text,
		// Tavily caps max_results at 20.
		"max_results":  min(q.Limit, 20),
		"search_depth": "basic",
	})
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+q.Key)
	var body struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := search.JSON(ctx, t.client, req, &body); err != nil {
		return nil, err
	}
	out := make([]search.Result, len(body.Results))
	for i, r := range body.Results {
		out[i] = search.Result{Title: r.Title, URL: r.URL, Description: search.Clean(r.Content)}
	}
	return out, nil
}

// Exa searches the Exa API. It needs a key.
type Exa struct{ client *http.Client }

// NewExa returns an Exa provider.
func NewExa(client *http.Client) *Exa { return &Exa{client: client} }

// Search runs the query on Exa.
func (e *Exa) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	req, err := search.Post(ctx, "https://api.exa.ai/search", map[string]any{
		"query":      q.Text,
		"numResults": min(q.Limit, 100),
		"type":       "auto",
		// Highlights only: whole-page text would blow the token budget.
		"contents": map[string]any{"highlights": map[string]any{"maxCharacters": 400}},
	})
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", q.Key)
	var body struct {
		Results []struct {
			Title      string   `json:"title"`
			URL        string   `json:"url"`
			Highlights []string `json:"highlights"`
		} `json:"results"`
	}
	if err := search.JSON(ctx, e.client, req, &body); err != nil {
		return nil, err
	}
	out := make([]search.Result, len(body.Results))
	for i, r := range body.Results {
		title := r.Title
		if title == "" {
			title = r.URL
		}
		out[i] = search.Result{Title: title, URL: r.URL, Description: search.Clean(strings.Join(r.Highlights, " "))}
	}
	return out, nil
}

// Brave searches the Brave Search API. It needs a key.
type Brave struct{ client *http.Client }

// NewBrave returns a Brave provider.
func NewBrave(client *http.Client) *Brave { return &Brave{client: client} }

// Search runs the query on Brave.
func (b *Brave) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	params := url.Values{"q": {q.Text}, "count": {strconv.Itoa(min(q.Limit, 20))}}
	req, err := search.Get(ctx, "https://api.search.brave.com/res/v1/web/search?"+params.Encode())
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", q.Key)
	var body struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := search.JSON(ctx, b.client, req, &body); err != nil {
		return nil, err
	}
	out := make([]search.Result, len(body.Web.Results))
	for i, r := range body.Web.Results {
		out[i] = search.Result{Title: r.Title, URL: r.URL, Description: search.Clean(r.Description)}
	}
	return out, nil
}

// Marginalia searches Marginalia's public API, which needs no key and
// favours the small, non-commercial web.
type Marginalia struct{ client *http.Client }

// NewMarginalia returns a Marginalia provider.
func NewMarginalia(client *http.Client) *Marginalia { return &Marginalia{client: client} }

// Search runs the query on Marginalia.
func (m *Marginalia) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	req, err := search.Get(ctx, "https://api.marginalia.nu/public/search/"+url.PathEscape(q.Text))
	if err != nil {
		return nil, err
	}
	var body struct {
		Results []struct {
			URL         string `json:"url"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := search.JSON(ctx, m.client, req, &body); err != nil {
		return nil, err
	}
	out := make([]search.Result, 0, min(len(body.Results), q.Limit))
	for _, r := range body.Results[:min(len(body.Results), q.Limit)] {
		out = append(out, search.Result{Title: r.Title, URL: r.URL, Description: search.Clean(r.Description)})
	}
	return out, nil
}

var (
	_ search.Searcher = (*Tavily)(nil)
	_ search.Searcher = (*Exa)(nil)
	_ search.Searcher = (*Brave)(nil)
	_ search.Searcher = (*Marginalia)(nil)
)
