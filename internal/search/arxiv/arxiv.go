package arxiv

import (
	"cmp"
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/erlidev/eika/internal/search"
)

// DefaultURL is the host arXiv asks API clients to use.
const DefaultURL = "https://export.arxiv.org"

// maxResponseBytes bounds an Atom answer. Twenty-five abstracts are a small
// fraction of it.
const maxResponseBytes = 1_000_000

var (
	// fieldPrefix finds arXiv's own field syntax in a query.
	fieldPrefix = regexp.MustCompile(`(?i)(?:^|[\s(])(?:ti|au|abs|co|jr|cat|rn|id|all|submittedDate):`)
	// term is a quoted phrase or a word.
	term = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|\S+`)
)

// Source searches arXiv.
type Source struct {
	client *http.Client
	base   string
}

// New returns a Source on the arXiv API at baseURL.
func New(client *http.Client, baseURL string) *Source {
	return &Source{client: client, base: strings.TrimRight(baseURL, "/")}
}

// Query turns natural text into terms that must all match in any field, and
// leaves a query in arXiv's field syntax alone.
func Query(text string) string {
	if fieldPrefix.MatchString(text) {
		return text
	}
	terms := term.FindAllString(text, -1)
	for i, t := range terms {
		terms[i] = "all:" + t
	}
	return strings.Join(terms, " AND ")
}

// feed is the part of arXiv's Atom answer the source reads.
type feed struct {
	Entries []entry `xml:"http://www.w3.org/2005/Atom entry"`
}

// entry is one paper, or the error arXiv reports as an entry.
type entry struct {
	ID        string `xml:"http://www.w3.org/2005/Atom id"`
	Title     string `xml:"http://www.w3.org/2005/Atom title"`
	Summary   string `xml:"http://www.w3.org/2005/Atom summary"`
	Published string `xml:"http://www.w3.org/2005/Atom published"`
	Authors   []struct {
		Name string `xml:"http://www.w3.org/2005/Atom name"`
	} `xml:"http://www.w3.org/2005/Atom author"`
	Links []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"http://www.w3.org/2005/Atom link"`
	Category struct {
		Term string `xml:"term,attr"`
	} `xml:"http://arxiv.org/schemas/atom primary_category"`
}

// Search runs the query, best match first.
func (s *Source) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	params := url.Values{
		"search_query": {Query(q.Text)},
		"start":        {"0"},
		"max_results":  {strconv.Itoa(q.Limit)},
		"sortBy":       {"relevance"},
		"sortOrder":    {"descending"},
	}
	req, err := search.Get(ctx, s.base+"/api/query?"+params.Encode())
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/atom+xml")
	body, truncated, err := search.Do(ctx, s.client, req, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, search.Errorf("arXiv returned an oversized Atom response")
	}
	var f feed
	if err := xml.Unmarshal(body, &f); err != nil {
		return nil, search.Errorf("arXiv returned invalid Atom XML")
	}
	out := make([]search.Result, 0, len(f.Entries))
	for _, e := range f.Entries {
		if strings.Contains(e.ID, "/api/errors#") {
			return nil, search.Errorf("arXiv rejected the query: %s", cmp.Or(search.Clean(e.Summary), "unknown API error"))
		}
		out = append(out, result(e))
	}
	return out, nil
}

// result renders one paper: its authors, date, and category, then its
// abstract.
func result(e entry) search.Result {
	var authors []string
	for _, a := range e.Authors {
		if name := strings.TrimSpace(a.Name); name != "" {
			authors = append(authors, name)
		}
	}
	byline := strings.Join(authors, ", ")
	if len(authors) > 3 {
		byline = strings.Join(authors[:3], ", ") + ", et al."
	}
	published := strings.TrimSpace(e.Published)
	published = published[:min(len(published), 10)]
	var parts []string
	for _, p := range []string{byline, published, e.Category.Term, strings.TrimSpace(e.Summary)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	link := strings.TrimSpace(e.ID)
	for _, l := range e.Links {
		if l.Rel == "alternate" && l.Href != "" {
			link = l.Href
			break
		}
	}
	return search.Result{
		Title:       search.Clean(e.Title),
		URL:         secure(link),
		Description: search.Clean(strings.Join(parts, " · ")),
	}
}

// secure upgrades an arxiv.org link to https, which the feed does not use.
func secure(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	if host := u.Hostname(); host == "arxiv.org" || strings.HasSuffix(host, ".arxiv.org") {
		u.Scheme = "https"
	}
	return u.String()
}

var _ search.Searcher = (*Source)(nil)
