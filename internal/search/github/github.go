package github

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/erlidev/eika/internal/search"
)

// DefaultURL is GitHub's API.
const DefaultURL = "https://api.github.com"

// Bucket is the quota and cooldown bucket every GitHub API call counts
// against, searches and fetch's reads alike.
const Bucket = "github"

// Kind is what a GitHub search looks through.
type Kind string

// The kinds of GitHub search.
const (
	Code   Kind = "code"
	Repos  Kind = "repositories"
	Issues Kind = "issues"
)

// Header sets the headers every GitHub API call sends: the API version, the
// media type, and the token when there is one.
func Header(h http.Header, accept, token string) {
	h.Set("Accept", accept)
	h.Set("X-GitHub-Api-Version", "2022-11-28")
	h.Set("User-Agent", search.UserAgent)
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
}

// Source searches one kind of GitHub content.
type Source struct {
	client *http.Client
	base   string
	kind   Kind
}

// New returns a Source searching kind on the API at baseURL.
func New(client *http.Client, baseURL string, kind Kind) *Source {
	return &Source{client: client, base: strings.TrimRight(baseURL, "/"), kind: kind}
}

// Search runs the query in GitHub's own syntax, qualifiers and all.
func (s *Source) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	if s.kind == Repos {
		if err := repositoryQualifiers(q.Text); err != nil {
			return nil, err
		}
	}
	params := url.Values{"q": {q.Text}, "per_page": {strconv.Itoa(min(q.Limit, 50))}}
	if s.kind == Issues {
		params.Set("advanced_search", "true")
	}
	req, err := search.Get(ctx, s.base+"/search/"+string(s.kind)+"?"+params.Encode())
	if err != nil {
		return nil, err
	}
	accept := "application/vnd.github+json"
	if s.kind == Code {
		// Text matches carry each hit's matching lines, so a code result has a
		// snippet without a second request per file.
		accept = "application/vnd.github.text-match+json"
	}
	Header(req.Header, accept, q.Key)
	var body struct {
		Items []item `json:"items"`
	}
	if err := search.JSON(ctx, s.client, req, &body); err != nil {
		return nil, err
	}
	out := make([]search.Result, len(body.Items))
	for i, it := range body.Items {
		out[i] = s.result(it)
	}
	return out, nil
}

// item is a code, repository, or issue hit; each kind fills its own fields.
type item struct {
	HTMLURL string `json:"html_url"`
	// Code.
	Path       string `json:"path"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	TextMatches []struct {
		Fragment string `json:"fragment"`
	} `json:"text_matches"`
	// Repositories.
	FullName    string  `json:"full_name"`
	Description *string `json:"description"`
	Stars       *int    `json:"stargazers_count"`
	Language    *string `json:"language"`
	// Issues. PullRequest is present only on a pull request: the issues
	// endpoint returns both, with no other marker.
	Title         string  `json:"title"`
	Number        int     `json:"number"`
	State         string  `json:"state"`
	Body          *string `json:"body"`
	RepositoryURL string  `json:"repository_url"`
	PullRequest   any     `json:"pull_request"`
}

// result renders one hit.
func (s *Source) result(it item) search.Result {
	switch s.kind {
	case Code:
		fragments := make([]string, len(it.TextMatches))
		for i, m := range it.TextMatches {
			fragments[i] = m.Fragment
		}
		return search.Result{
			Title:       strings.TrimPrefix(it.Repository.FullName+"/"+it.Path, "/"),
			URL:         it.HTMLURL,
			Description: search.Clean(strings.Join(fragments, " … ")),
		}
	case Repos:
		var parts []string
		if it.Stars != nil {
			parts = append(parts, "★"+strconv.Itoa(*it.Stars))
		}
		for _, p := range []*string{it.Language, it.Description} {
			if p != nil && *p != "" {
				parts = append(parts, *p)
			}
		}
		return search.Result{Title: it.FullName, URL: it.HTMLURL, Description: search.Clean(strings.Join(parts, " · "))}
	}
	_, repo, _ := strings.Cut(it.RepositoryURL, "/repos/")
	title := repo + "#" + strconv.Itoa(it.Number)
	if it.PullRequest != nil {
		title += " (PR)"
	}
	var parts []string
	for _, p := range []string{it.State, deref(it.Body)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return search.Result{
		Title:       strings.TrimSpace(title + " " + it.Title),
		URL:         it.HTMLURL,
		Description: search.Clean(strings.Join(parts, " · ")),
	}
}

// deref returns what p points at, empty for nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// nonRepository lists the qualifiers of code and issue search, which
// repository search does not understand.
var nonRepository = []string{
	"assignee", "author", "base", "commenter", "committer", "extension", "filename", "hash",
	"head", "involves", "label", "mentions", "milestone", "owner", "path", "repo",
	"review-requested", "reviewed-by", "state", "team-review-requested", "type",
}

// nonRepositoryIs lists the is: values that name issues or pull requests.
var nonRepositoryIs = []string{"pr", "issue", "open", "closed", "merged", "unmerged", "draft", "locked"}

// qualifier finds a qualifier and its value.
var qualifier = regexp.MustCompile(`(?:^|\s)([\w-]+):("[^"]*"|\S+)`)

// repositoryQualifiers refuses a repository search carrying qualifiers that
// only code or issue search understands, which GitHub would ignore silently.
func repositoryQualifiers(query string) error {
	var found []string
	for _, m := range qualifier.FindAllStringSubmatch(query, -1) {
		name := strings.ToLower(m[1])
		value := strings.ToLower(strings.Trim(m[2], `"`))
		if (slices.Contains(nonRepository, name) || name == "is" && slices.Contains(nonRepositoryIs, value)) &&
			!slices.Contains(found, name+":") {
			found = append(found, name+":")
		}
	}
	if len(found) == 0 {
		return nil
	}
	pronoun := "it"
	if len(found) > 1 {
		pronoun = "them"
	}
	return search.Errorf("GitHub repository search does not support %s; remove %s or use github_code/github_issues",
		strings.Join(found, ", "), pronoun)
}

var _ search.Searcher = (*Source)(nil)
