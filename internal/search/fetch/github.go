package fetch

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/github"
	"github.com/erlidev/eika/internal/search/page"
)

// Media types of the GitHub API reads.
const (
	githubJSON = "application/vnd.github+json"
	githubRaw  = "application/vnd.github.raw"
	githubDiff = "application/vnd.github.diff"
)

// token reads the GitHub token, empty when none is stored.
func (r *Reader) token(ctx context.Context) (string, error) {
	if r.cfg.Keys == nil {
		return "", nil
	}
	t, err := r.cfg.Keys.SearchKey(ctx, "github")
	if err != nil {
		return "", fmt.Errorf("read the github key: %w", err)
	}
	return t, nil
}

// github runs a GitHub read against the bucket the GitHub sources count in.
// It counts one use per read, not per request: an issue is two requests and
// one read.
func (r *Reader) github(ctx context.Context, p plan) (fetched, error) {
	var limit search.Limit
	if r.cfg.Quota != nil {
		var err error
		if limit, err = r.cfg.Quota.Limit(ctx, github.Bucket); err != nil {
			return fetched{}, err
		}
	}
	if blocked := r.cfg.Tracker.Blocked(github.Bucket, limit, r.cfg.Now()); blocked != "" {
		return fetched{}, search.Errorf("GitHub %s", blocked)
	}
	token, err := r.token(ctx)
	if err != nil {
		return fetched{}, err
	}
	pg, err := r.githubRead(ctx, p, token)
	if err != nil {
		var h *search.HTTPError
		if ctx.Err() == nil && errors.As(err, &h) && (h.Status == http.StatusForbidden || h.Status == http.StatusTooManyRequests) {
			now := r.cfg.Now()
			r.cfg.Tracker.RecordFailure(ctx, github.Bucket, now, search.RetryDeadline(err, now))
		}
		return fetched{}, err
	}
	r.cfg.Tracker.RecordUse(ctx, github.Bucket, r.cfg.Now())
	return pg, nil
}

// githubRead assembles one GitHub read as Markdown.
func (r *Reader) githubRead(ctx context.Context, p plan, token string) (fetched, error) {
	pg := fetched{Title: p.label, ContentType: "text/markdown"}
	repo := "/repos/" + url.PathEscape(p.owner) + "/" + url.PathEscape(p.repo)
	text := func(path, accept string) (string, error) {
		resp, err := r.get(ctx, r.cfg.GitHubAPI+path, githubHeader(accept, token))
		if err != nil {
			return "", err
		}
		pg.Bytes, pg.Truncated = len(resp.body), resp.truncated
		return decode(resp.body, resp.contentType)
	}
	decodeJSON := func(path string, out any) error {
		resp, err := r.get(ctx, r.cfg.GitHubAPI+path, githubHeader(githubJSON, token))
		if err != nil {
			return err
		}
		if resp.truncated || json.Unmarshal(resp.body, out) != nil {
			return search.Errorf("GitHub answered %s with a response that could not be read", path)
		}
		return nil
	}

	switch p.op {
	case opReadme:
		body, err := text(repo+"/readme", githubRaw)
		pg.Markdown = body
		return pg, err

	case opContent:
		body, err := text(p.apiPath, githubRaw)
		pg.Markdown = body
		if p.lang != "" {
			pg.Markdown = page.Fence(body, p.lang)
		}
		return pg, err

	case opTree:
		path := repo + "/contents/" + escapePath(p.path)
		if p.ref != "" {
			path += "?ref=" + url.QueryEscape(p.ref)
		}
		var raw json.RawMessage
		if err := decodeJSON(path, &raw); err != nil {
			return pg, err
		}
		var entries []treeEntry
		if json.Unmarshal(raw, &entries) != nil {
			var one treeEntry
			if err := json.Unmarshal(raw, &one); err != nil {
				return pg, search.Errorf("GitHub answered %s with a response that could not be read", path)
			}
			entries = []treeEntry{one}
		}
		pg.Markdown = fmt.Sprintf("# %s: /%s\n\n%s", p.label, p.path, cmp.Or(listing(entries), "(empty)"))
		return pg, nil

	case opIssue:
		base := fmt.Sprintf("%s/issues/%d", repo, p.number)
		var issue struct {
			Title       string  `json:"title"`
			State       string  `json:"state"`
			Body        *string `json:"body"`
			User        user    `json:"user"`
			CreatedAt   string  `json:"created_at"`
			PullRequest any     `json:"pull_request"`
		}
		if err := decodeJSON(base, &issue); err != nil {
			return pg, err
		}
		var comments []struct {
			Body      *string `json:"body"`
			User      user    `json:"user"`
			CreatedAt string  `json:"created_at"`
		}
		if err := decodeJSON(base+"/comments?per_page=50", &comments); err != nil {
			return pg, err
		}
		kind := "issue"
		if issue.PullRequest != nil {
			kind = "pull request"
		}
		parts := []string{
			"# " + issue.Title,
			fmt.Sprintf("%s · %s · %s", p.label, kind, cmp.Or(issue.State, "?")),
			"",
			attribution(issue.User.Login, issue.CreatedAt),
			"",
			cmp.Or(strings.TrimSpace(deref(issue.Body)), "_No description._"),
		}
		for _, c := range comments {
			parts = append(parts, "", "---", "", attribution(c.User.Login, c.CreatedAt), "", strings.TrimSpace(deref(c.Body)))
		}
		pg.Title, pg.Markdown = issue.Title, strings.Join(parts, "\n")
		return pg, nil

	case opDiff:
		body, err := text(fmt.Sprintf("%s/pulls/%d", repo, p.number), githubDiff)
		pg.Markdown = page.Fence(body, "diff")
		return pg, err

	case opRelease:
		var rel struct {
			Name        *string `json:"name"`
			TagName     string  `json:"tag_name"`
			PublishedAt string  `json:"published_at"`
			Body        *string `json:"body"`
		}
		if err := decodeJSON(repo+"/releases/tags/"+url.PathEscape(p.tag), &rel); err != nil {
			return pg, err
		}
		heading := cmp.Or(deref(rel.Name), rel.TagName, p.tag)
		pg.Title = heading
		pg.Markdown = strings.Join([]string{
			"# " + heading,
			p.owner + "/" + p.repo + " · " + day(rel.PublishedAt),
			"",
			cmp.Or(strings.TrimSpace(deref(rel.Body)), "_No release notes._"),
		}, "\n")
		return pg, nil

	case opGist:
		var gist struct {
			Description *string `json:"description"`
			Files       map[string]struct {
				Filename string  `json:"filename"`
				Language *string `json:"language"`
				Content  string  `json:"content"`
			} `json:"files"`
		}
		if err := decodeJSON("/gists/"+url.PathEscape(p.gist), &gist); err != nil {
			return pg, err
		}
		parts := []string{"# " + cmp.Or(strings.TrimSpace(deref(gist.Description)), "gist "+p.gist)}
		names := make([]string, 0, len(gist.Files))
		for name := range gist.Files {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			f := gist.Files[name]
			parts = append(parts, "", "## "+cmp.Or(f.Filename, "untitled"), "", page.Fence(f.Content, strings.ToLower(deref(f.Language))))
		}
		pg.Markdown = strings.Join(parts, "\n")
		return pg, nil
	}
	return pg, fmt.Errorf("unknown GitHub read %d", p.op)
}

// githubHeader is the header of one GitHub API request.
func githubHeader(accept, token string) http.Header {
	h := http.Header{}
	github.Header(h, accept, token)
	return h
}

// user is a GitHub account as the API names it.
type user struct {
	Login string `json:"login"`
}

// treeEntry is one entry of a directory listing.
type treeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size *int   `json:"size"`
}

// listing renders a directory as ls would order it: directories first, then
// files, each alphabetically.
func listing(entries []treeEntry) string {
	slices.SortFunc(entries, func(a, b treeEntry) int {
		if (a.Type == "dir") != (b.Type == "dir") {
			if a.Type == "dir" {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	lines := make([]string, len(entries))
	for i, e := range entries {
		switch {
		case e.Type == "dir":
			lines[i] = "- " + e.Name + "/"
		case e.Size != nil:
			lines[i] = fmt.Sprintf("- %s (%d bytes)", e.Name, *e.Size)
		default:
			lines[i] = "- " + e.Name
		}
	}
	return strings.Join(lines, "\n")
}

// attribution is a post's author and date line.
func attribution(login, at string) string {
	return strings.TrimSpace(fmt.Sprintf("**%s** · %s", cmp.Or(login, "unknown"), day(at)))
}

// day cuts a timestamp to its date.
func day(at string) string { return at[:min(len(at), 10)] }

// deref returns what p points at, empty for nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// escapePath escapes each segment of a repository path.
func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
