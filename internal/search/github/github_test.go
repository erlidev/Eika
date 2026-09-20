package github_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/github"
	"github.com/erlidev/eika/internal/search/searchtest"
)

func TestCodeSearchUsesTextMatches(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"items": []map[string]any{{
		"path": "src/lib.rs", "html_url": "https://github.com/tokio-rs/tokio/blob/master/src/lib.rs",
		"repository":   map[string]string{"full_name": "tokio-rs/tokio"},
		"text_matches": []map[string]string{{"fragment": "pub fn spawn"}, {"fragment": "fn block_on"}},
	}}}))
	got, err := github.New(client, github.DefaultURL, github.Code).Search(t.Context(), search.Query{Text: "spawn language:rust", Limit: 5, Key: "ghp_x"})
	if err != nil {
		t.Fatal(err)
	}
	want := []search.Result{{
		Title: "tokio-rs/tokio/src/lib.rs", URL: "https://github.com/tokio-rs/tokio/blob/master/src/lib.rs",
		Description: "pub fn spawn … fn block_on",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("results = %+v", got)
	}
	req := tr.Requests()[0]
	if req.URL.Path != "/search/code" || req.Header.Get("Accept") != "application/vnd.github.text-match+json" ||
		req.Header.Get("Authorization") != "Bearer ghp_x" || req.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
		t.Errorf("request = %s %v", req.URL, req.Header)
	}
}

func TestRepoSearchSummarisesStarsAndLanguage(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"items": []map[string]any{
		{"full_name": "tokio-rs/tokio", "html_url": "https://github.com/tokio-rs/tokio", "stargazers_count": 27000, "language": "Rust", "description": "A runtime"},
		{"full_name": "x/y", "html_url": "https://github.com/x/y", "stargazers_count": 0, "language": nil, "description": nil},
	}}))
	got, err := github.New(client, github.DefaultURL, github.Repos).Search(t.Context(), search.Query{Text: "tokio language:rust", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Description != "★27000 · Rust · A runtime" || got[1].Description != "★0" {
		t.Errorf("results = %+v", got)
	}
	if req := tr.Requests()[0]; req.Header.Get("Authorization") != "" || req.URL.Path != "/search/repositories" {
		t.Errorf("request = %s %v", req.URL, req.Header)
	}
}

func TestRepoSearchRejectsOtherEndpointsQualifiers(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{}))
	s := github.New(client, github.DefaultURL, github.Repos)
	_, err := s.Search(t.Context(), search.Query{Text: `tokio is:pr label:"bug" Label:x`, Limit: 5})
	if err == nil || err.Error() != "GitHub repository search does not support is:, label:; remove them or use github_code/github_issues" {
		t.Errorf("err = %v", err)
	}
	if len(tr.Requests()) != 0 {
		t.Error("an invalid query spent a request")
	}
	if _, err := s.Search(t.Context(), search.Query{Text: "tokio stars:>100 is:public topic:async", Limit: 5}); err != nil {
		t.Errorf("repository qualifiers were refused: %v", err)
	}
}

func TestIssueSearch(t *testing.T) {
	client, tr := searchtest.Client(searchtest.JSON(map[string]any{"items": []map[string]any{
		{"title": "Memory leak", "html_url": "https://github.com/tokio-rs/tokio/issues/3481", "number": 3481, "state": "closed",
			"body": "Observed growth under load", "repository_url": "https://api.github.com/repos/tokio-rs/tokio"},
		{"title": "Fix the leak", "html_url": "https://github.com/tokio-rs/tokio/pull/8158", "number": 8158, "state": "open",
			"body": nil, "repository_url": "https://api.github.com/repos/tokio-rs/tokio", "pull_request": map[string]any{}},
	}}))
	got, err := github.New(client, github.DefaultURL, github.Issues).Search(t.Context(), search.Query{Text: "leak", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Title != "tokio-rs/tokio#3481 Memory leak" || got[0].Description != "closed · Observed growth under load" {
		t.Errorf("issue = %+v", got[0])
	}
	if got[1].Title != "tokio-rs/tokio#8158 (PR) Fix the leak" || got[1].Description != "open" {
		t.Errorf("pull request = %+v", got[1])
	}
	if q := tr.Requests()[0].URL.RawQuery; !strings.Contains(q, "advanced_search=true") {
		t.Errorf("query = %s", q)
	}
}
