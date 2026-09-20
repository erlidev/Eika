package search

import "time"

// Searchers are the built-in backends, built by the caller so that this
// package imports none of them.
type Searchers struct {
	SearxNG, Exa, Tavily, Brave, Marginalia Searcher
	Wikipedia, Arxiv                        Searcher
	GitHubCode, GitHubRepos, GitHubIssues   Searcher
}

// Registry registers the built-in backends: the web providers in their
// default order, then the sources. Adding a backend is one entry here.
func Registry(s Searchers) []Backend {
	return []Backend{
		{Name: "searxng", Searcher: s.SearxNG, Web: true},
		{Name: "exa", Searcher: s.Exa, Web: true, Key: "exa", KeyRequired: true, Limit: Limit{Month: 900}},
		{Name: "tavily", Searcher: s.Tavily, Web: true, Key: "tavily", KeyRequired: true, Limit: Limit{Month: 1000}},
		{Name: "brave", Searcher: s.Brave, Web: true, Key: "brave", KeyRequired: true, Limit: Limit{Month: 2000}},
		{Name: "marginalia", Searcher: s.Marginalia, Web: true, Limit: Limit{Day: 100}},

		{Name: "wikipedia", Searcher: s.Wikipedia},
		// arXiv asks for one request at a time, three seconds apart. The pool
		// lets a later, larger count for the same query skip the wait.
		{Name: "arxiv", Searcher: s.Arxiv, Pool: MaxCount, Interval: 3 * time.Second},
		// The three GitHub sources and fetch's GitHub reads share a bucket, so
		// one rate limit cools them all down. Only code search needs a token.
		{Name: "github_code", Searcher: s.GitHubCode, Key: "github", KeyRequired: true, Bucket: "github"},
		{Name: "github_repos", Searcher: s.GitHubRepos, Key: "github", Bucket: "github"},
		{Name: "github_issues", Searcher: s.GitHubIssues, Key: "github", Bucket: "github"},
	}
}
