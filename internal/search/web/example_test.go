package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/searchtest"
)

// Kagi is the worked example from docs/EXTENDING.md: a keyed web provider.
// Keeping it compiled here keeps the documentation honest.
type Kagi struct{ client *http.Client }

// NewKagi returns a Kagi provider.
func NewKagi(client *http.Client) *Kagi { return &Kagi{client: client} }

// Search runs the query on Kagi's search API.
func (k *Kagi) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	params := url.Values{"q": {q.Text}, "limit": {strconv.Itoa(q.Limit)}}
	req, err := search.Get(ctx, "https://kagi.com/api/v0/search?"+params.Encode())
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+q.Key)
	var body struct {
		Data []struct {
			T       int    `json:"t"`
			URL     string `json:"url"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"data"`
	}
	if err := search.JSON(ctx, k.client, req, &body); err != nil {
		return nil, err
	}
	var out []search.Result
	for _, d := range body.Data {
		if d.T == 0 { // 0 is a search result; other types are related searches
			out = append(out, search.Result{Title: d.Title, URL: d.URL, Description: search.Clean(d.Snippet)})
		}
	}
	return out, nil
}

func TestTheExtendingExampleRegistersAndSearches(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"data": []map[string]any{
		{"t": 0, "url": "https://tokio.rs/", "title": "Tokio", "snippet": "An <b>async</b> runtime"},
		{"t": 1, "list": []string{"tokio tutorial"}},
	}}))
	engine, err := search.NewEngine(search.Config{
		Backends: []search.Backend{
			{Name: "kagi", Searcher: NewKagi(client), Web: true, Key: "kagi", KeyRequired: true, Limit: search.Limit{Month: 100}},
		},
		Keys: keyring{"kagi": "k"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := engine.Search(t.Context(), search.Request{Query: "tokio"})
	if err != nil || out.Text != "1. Tokio\n   https://tokio.rs/\n   An async runtime" {
		t.Errorf("Search = %q, %v", out.Text, err)
	}
	if got := tr.Requests()[0].Header.Get("Authorization"); got != "Bot k" {
		t.Errorf("authorization = %q", got)
	}
}

// keyring serves fixed keys.
type keyring map[string]string

func (k keyring) SearchKey(_ context.Context, name string) (string, error) { return k[name], nil }
