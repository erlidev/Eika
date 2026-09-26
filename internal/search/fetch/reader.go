package fetch

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"

	"github.com/erlidev/eika/internal/netguard"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/github"
	"github.com/erlidev/eika/internal/search/page"
)

// Bounds on one fetch.
const (
	// fetchTimeout bounds one request. Pages are slower to serve than
	// search APIs.
	fetchTimeout = 20 * time.Second
	// maxBodyBytes is how much of a response is read. Past it the body is
	// cut, and the details say so.
	maxBodyBytes = 2_000_000
	// cacheTTL is how long a fetched page is served from the cache, which
	// is what makes a retried filter free.
	cacheTTL = 6 * time.Hour
	// cacheSize bounds the pages the cache holds.
	cacheSize = 64
	// maxURLBytes bounds the URL a fetch accepts.
	maxURLBytes = 2048
	// maxSectionRunes bounds a section name.
	maxSectionRunes = 500
	// maxFilterBytes bounds a filter's source.
	maxFilterBytes = 10_000
)

// Accept headers. text/plain is left out of the HTML one on purpose: a
// content-negotiating API asked for plain text sends the same JSON labelled
// text/plain, which defeats the JSON handling.
const (
	acceptHTML = "text/html,application/xhtml+xml;q=0.9,text/markdown;q=0.8,*/*;q=0.5"
	acceptText = "text/plain,text/markdown,*/*;q=0.8"
)

// The formats a fetch returns.
const (
	// Markdown is the page as Markdown. It is the default.
	Markdown = "markdown"
	// Text is the answer with its markup stripped.
	Text = "text"
	// Raw is the response body as it came.
	Raw = "raw"
)

// Quota reads a bucket's quota under the settings in force. search.Engine
// implements it.
type Quota interface {
	Limit(ctx context.Context, bucket string) (search.Limit, error)
}

// Config is what a Reader is built from.
type Config struct {
	// Client makes every request. Production passes NewClient.
	Client *http.Client
	// Tracker counts GitHub reads in the bucket the GitHub sources use.
	Tracker *search.Tracker
	// Keys supplies the GitHub token; Quota the GitHub bucket's limit.
	Keys  search.Keys
	Quota Quota
	// GitHubAPI is the GitHub API's base URL, github.DefaultURL when empty.
	GitHubAPI string
	// Now is the clock, time.Now when nil.
	Now func() time.Time
}

// Reader reads one URL as Markdown, narrowed to a section or by a filter,
// and fitted to the content budget.
type Reader struct {
	cfg   Config
	cache *search.Cache[fetched]
}

// New returns a Reader.
func New(cfg Config) *Reader {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.GitHubAPI = strings.TrimRight(cmp.Or(cfg.GitHubAPI, github.DefaultURL), "/")
	if cfg.Tracker == nil {
		cfg.Tracker, _ = search.NewTracker(context.Background(), nil, nil)
	}
	return &Reader{cfg: cfg, cache: search.NewCache[fetched](cacheSize, cacheTTL)}
}

// Cached returns how many pages the cache holds, for the status report.
func (r *Reader) Cached() int { return r.cache.Len() }

// Request is one fetch.
type Request struct {
	URL string `json:"url"`
	// Section names a heading to read, with its subsections.
	Section string `json:"section,omitempty"`
	// Filter is JavaScript run over the page in the sandbox.
	Filter string `json:"filter,omitempty"`
	// Format is Markdown, Text, or Raw; empty means Markdown.
	Format string `json:"format,omitempty"`
}

// FilterFunc runs a filter. The web_fetch tool runs it as `eikad filter`
// inside the workspace sandbox.
type FilterFunc func(ctx context.Context, req page.FilterRequest) (page.FilterOutcome, error)

// Outcome is a finished fetch. Text is what the model reads; IsError marks a
// fetch the model should see as failed.
type Outcome struct {
	Text    string
	IsError bool
	Details Details
}

// Details describe how a fetch went, for the interface. They never reach the
// model.
type Details struct {
	URL     string `json:"url"`
	Format  string `json:"format"`
	Section string `json:"section,omitempty"`
	Filter  string `json:"filter,omitempty"`
	// SectionMatched says whether the section, or the URL's fragment, named
	// a heading. A fragment that names none reads the whole page.
	SectionMatched *bool  `json:"section_matched,omitempty"`
	FinalURL       string `json:"final_url,omitempty"`
	// Container is what the content was extracted from: the thing to check
	// when a site extracts badly.
	Container   string `json:"container,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
	// BodyTruncated reports that the response was cut at the byte cap.
	BodyTruncated bool `json:"body_truncated,omitempty"`
	Cached        bool `json:"cached,omitempty"`
	// Mode is how the content met its budget: full, truncated, or outline.
	Mode            page.Mode `json:"mode,omitempty"`
	BudgetTruncated bool      `json:"budget_truncated,omitempty"`
	// Headings counts the headings a missed section could have named.
	Headings      int               `json:"headings,omitempty"`
	FilterOutcome string            `json:"filter_outcome,omitempty"`
	FilterStats   *page.FilterStats `json:"filter_stats,omitempty"`
	Ms            int64             `json:"ms"`
}

// Read runs one fetch: the page, from the cache when it is there, then the
// section, then the filter or the budget, then the header. A failure the
// model can act on is an Outcome with IsError set; the error is only for a
// cancelled fetch.
func (r *Reader) Read(ctx context.Context, req Request, filter FilterFunc) (Outcome, error) {
	started := r.cfg.Now()
	rawURL := strings.TrimSpace(req.URL)
	format := cmp.Or(strings.TrimSpace(req.Format), Markdown)
	asked := strings.TrimSpace(req.Section)
	expression := strings.TrimSpace(req.Filter)
	d := Details{URL: rawURL, Format: format, Section: asked, Filter: expression}
	done := func(text string, isError bool) (Outcome, error) {
		d.Ms = r.cfg.Now().Sub(started).Milliseconds()
		return Outcome{Text: text, IsError: isError, Details: d}, nil
	}

	switch {
	case rawURL == "":
		return done("fetch failed: url is empty.", true)
	case len(rawURL) > maxURLBytes:
		return done(fmt.Sprintf("fetch failed: url is longer than %d characters.", maxURLBytes), true)
	case format != Markdown && format != Text && format != Raw:
		return done("fetch failed: format must be markdown, text, or raw.", true)
	case asked != "" && format == Raw:
		return done("fetch failed: section needs markdown or text format, not raw.", true)
	case utf8.RuneCountInString(asked) > maxSectionRunes:
		return done(fmt.Sprintf("fetch failed: section is longer than %d characters.", maxSectionRunes), true)
	case len(expression) > maxFilterBytes:
		return done(fmt.Sprintf("fetch failed: filter is longer than %d characters.", maxFilterBytes), true)
	}

	// The section parameter and a URL fragment select the same way, but not
	// equally strongly. A fragment is whatever was on the end of a link, and
	// plenty point at a footnote or a table row rather than a heading, so a
	// fragment that names nothing means the page. Nothing is extracted in
	// raw format, so there are no headings for a fragment to name.
	section, required := asked, asked != ""
	if section == "" && format != Raw {
		section = page.Fragment(rawURL)
	}
	d.Section = section
	// Text is a rendering of the answer, applied after selection, the
	// filter, and the budget, all of which key off the headings it strips.
	render := func(s string) string { return s }
	if format == Text {
		render = page.PlainText
	}

	pg, err := r.page(ctx, rawURL, format)
	if err != nil {
		if ctx.Err() != nil {
			return Outcome{}, ctx.Err()
		}
		return done("fetch failed: "+search.Describe(err), true)
	}
	d.FinalURL, d.Container, d.ContentType = pg.FinalURL, pg.Container, pg.ContentType
	d.Bytes, d.BodyTruncated, d.Cached = pg.Bytes, pg.Truncated, pg.cached

	scoped, narrowed := pg.Markdown, false
	if section != "" {
		pick := page.Select(pg.Markdown, section)
		d.SectionMatched = &pick.Found
		if !pick.Found && required {
			d.Headings = len(pick.Available)
			return done(noSection(section, pick.Available), true)
		}
		if pick.Found {
			scoped, narrowed = pick.Text, true
		}
	}

	if expression != "" {
		if filter == nil {
			return done("fetch failed: filters cannot run here.", true)
		}
		out, err := filter(ctx, page.FilterRequest{
			Markdown:  scoped,
			Source:    expression,
			TimeoutMS: page.FilterTimeout.Milliseconds(),
			Tokens:    page.ContentTokens,
		})
		if err != nil {
			if ctx.Err() != nil {
				return Outcome{}, ctx.Err()
			}
			return done("fetch failed: the filter could not run in the sandbox: "+err.Error(), true)
		}
		d.FilterOutcome, d.FilterStats, d.BudgetTruncated = out.Kind, &out.Stats, out.Truncated
		if out.Kind != page.FilterOK {
			// A filter that matched nothing is a result, not a failure: the
			// page map it returns is what the retry is written against.
			return done(out.Text, out.Kind == page.FilterError)
		}
		// Returned bare: everything prepended to a narrow answer is a token
		// the model did not ask for.
		return done(render(out.Text)+out.Footer, false)
	}

	shaped := page.Shape(scoped, page.ContentTokens, narrowed)
	d.Mode, d.BudgetTruncated = shaped.Mode, shaped.Mode != page.Full
	body := shaped.Text
	// An outline is a map, not prose: its # nesting tells two similar
	// headings apart, and text would flatten it.
	if shaped.Mode != page.Outline {
		body = render(body)
	}
	return done(header(pg, render)+body, false)
}

// header is a title when the content does not open with one, and where the
// server sent the fetch when that is not where it asked. Both are otherwise
// wasted tokens.
func header(pg fetched, render func(string) string) string {
	var lines []string
	if pg.Title != "" && !strings.HasPrefix(pg.Markdown, "# ") {
		lines = append(lines, "# "+pg.Title)
	}
	// Compared with what was requested, not with what the model asked for:
	// a GitHub URL rewritten to the raw host went where it was told.
	if pg.FinalURL != "" && search.NormalizeURL(pg.FinalURL) != search.NormalizeURL(pg.RequestedURL) {
		lines = append(lines, "_Redirected to "+pg.FinalURL+"_")
	}
	if len(lines) == 0 {
		return ""
	}
	return render(strings.Join(lines, "\n")) + "\n\n"
}

// noSection is the error for a section the page does not have.
func noSection(wanted string, available []string) string {
	if len(available) == 0 {
		return fmt.Sprintf("fetch: this page has no headings, so %q cannot be selected.", wanted)
	}
	return fmt.Sprintf("fetch: no section matching %q. Available: %s", wanted, page.HeadingList(available))
}

// fetched is a page as the cache holds it.
type fetched struct {
	// RequestedURL is what was requested after planning; it differs from
	// the fetched URL when the plan rewrote it.
	RequestedURL string
	// FinalURL is where redirects ended.
	FinalURL    string
	Title       string
	Markdown    string
	ContentType string
	Container   string
	Bytes       int
	Truncated   bool
	cached      bool
}

// page fetches a URL, or serves it from the cache. Text shares Markdown's
// entry, since it is rendered from the same Markdown at the end; only raw
// fetches a different body.
func (r *Reader) page(ctx context.Context, rawURL, format string) (fetched, error) {
	target, err := validate(rawURL)
	if err != nil {
		return fetched{}, err
	}
	kind := "md"
	if format == Raw {
		kind = "raw"
	}
	key := "fetch|" + kind + "|" + search.NormalizeURL(target.String())
	if hit, ok := r.cache.Get(key, r.cfg.Now()); ok {
		hit.cached = true
		return hit, nil
	}
	pg, err := r.fetch(ctx, target, format)
	if err != nil {
		return fetched{}, err
	}
	r.cache.Put(key, pg, r.cfg.Now())
	return pg, nil
}

// privateName matches host names that only resolve on a private network.
var privateName = regexp.MustCompile(`(?i)^(localhost|.+\.(localhost|local|internal|home\.arpa))$`)

// validate refuses a URL that should not be dereferenced, before any
// request goes out. The client refuses a public name that resolves to a
// private address when it dials.
func validate(rawURL string) (*url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, search.Errorf("not a valid URL: %s", rawURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, search.Errorf("unsupported scheme %s; use http or https", u.Scheme)
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	addr, err := netip.ParseAddr(host)
	if privateName.MatchString(host) || err == nil && !netguard.IsPublic(addr) {
		return nil, search.Errorf("refusing to fetch a private address (%s)", host)
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

// githubContentHost reports whether u is served by GitHub's content hosts,
// the only ones the GitHub token goes to: githubusercontent.com or a
// subdomain of it, over https. A name that merely ends in the same letters,
// such as evilgithubusercontent.com, is somebody else's.
func githubContentHost(u *url.URL) bool {
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return u.Scheme == "https" && (host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com"))
}

// fetch reads a URL by its plan and dispatches on what the server sent.
func (r *Reader) fetch(ctx context.Context, target *url.URL, format string) (fetched, error) {
	p := planFor(target)
	if p.kind == planGitHub {
		pg, err := r.github(ctx, p)
		if err != nil {
			return fetched{}, err
		}
		// An API read went where it was told. The raw README and diff
		// media types answer with a documented redirect to a content host,
		// which is no news to the model.
		pg.RequestedURL, pg.FinalURL = target.String(), target.String()
		return pg, nil
	}

	header := http.Header{"Accept": {acceptHTML}}
	if p.kind == planText {
		header.Set("Accept", acceptText)
	}
	if u, err := url.Parse(p.url); err == nil && githubContentHost(u) {
		token, err := r.token(ctx)
		if err != nil {
			return fetched{}, err
		}
		if token != "" {
			header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := r.get(ctx, p.url, header)
	if err != nil {
		return fetched{}, err
	}
	pg := fetched{
		RequestedURL: p.url, FinalURL: resp.finalURL, ContentType: resp.contentType,
		Bytes: len(resp.body), Truncated: resp.truncated,
	}
	if format == Raw {
		pg.Markdown, err = decode(resp.body, resp.contentType)
		return pg, err
	}

	// Dispatch on what the server sent, not on what the URL implied: a .md
	// path is free to answer with an HTML rendering of the file.
	mediaType, _, _ := mime.ParseMediaType(resp.contentType)
	mediaType = strings.ToLower(mediaType)
	switch {
	case mediaType == "text/html", mediaType == "application/xhtml+xml", mediaType == "" && looksLikeHTML(resp.body):
		final, _ := url.Parse(resp.finalURL)
		out, err := extract(resp.body, resp.contentType, final)
		if err != nil {
			return fetched{}, err
		}
		pg.Title, pg.Markdown, pg.Container = out.title, out.markdown, out.container
		return pg, nil
	case mediaType == "application/json", strings.HasSuffix(mediaType, "+json"):
		pg.Markdown = page.Fence(pretty(resp.body), "json")
		return pg, nil
	case mediaType == "application/pdf":
		return fetched{}, search.Errorf("PDF is not supported; fetch the HTML version if one exists")
	case mediaType != "" && !strings.HasPrefix(mediaType, "text/"):
		return fetched{}, search.Errorf("unsupported content type %s (%d bytes)", mediaType, len(resp.body))
	}

	text, err := decode(resp.body, resp.contentType)
	if err != nil {
		return fetched{}, err
	}
	switch {
	case looksLikeJSON(text):
		// JSON labelled text/plain, which content-negotiating APIs and many
		// static hosts send. Taken only when it really parses.
		pg.Markdown = page.Fence(pretty([]byte(text)), "json")
	case p.kind == planText && p.lang != "":
		pg.Markdown = page.Fence(text, p.lang)
	default:
		pg.Markdown = text
	}
	return pg, nil
}

// response is what one request read.
type response struct {
	body        []byte
	truncated   bool
	contentType string
	finalURL    string
}

// get sends a GET with Eika's identity under the fetch timeout, and reads at
// most the byte cap of the body while it streams.
func (r *Reader) get(ctx context.Context, rawURL string, header http.Header) (response, error) {
	reqCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return response{}, search.Errorf("not a valid URL: %s", rawURL)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("User-Agent", search.UserAgent)
	req.Header.Set("Accept-Language", "en,*;q=0.5")
	resp, err := r.cfg.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return response{}, ctx.Err()
		}
		return response{}, transportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return response{}, &search.HTTPError{Status: resp.StatusCode, Message: fmt.Sprintf("HTTP %d", resp.StatusCode), Header: resp.Header}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return response{}, ctx.Err()
		}
		return response{}, transportError(err)
	}
	out := response{body: body, contentType: resp.Header.Get("Content-Type"), finalURL: resp.Request.URL.String()}
	if len(body) > maxBodyBytes {
		out.body, out.truncated = body[:maxBodyBytes], true
	}
	return out, nil
}

// transportError describes a request that got no answer.
func transportError(err error) error {
	if errors.Is(err, netguard.ErrPrivateAddress) {
		var op *net.OpError
		if errors.As(err, &op) && op.Addr != nil {
			host, _, _ := net.SplitHostPort(op.Addr.String())
			return search.Errorf("refusing to fetch a private address (%s)", host)
		}
		return search.Errorf("refusing to fetch a private address")
	}
	return &search.HTTPError{Message: search.DescribeNetwork(err)}
}

// decode reads a text body in its declared charset, or the one its meta tag
// names, falling back to UTF-8.
func decode(body []byte, contentType string) (string, error) {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return strings.ToValidUTF8(string(body), "�"), nil
	}
	text, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("decode body: %w", err)
	}
	return strings.ToValidUTF8(string(text), "�"), nil
}

// pretty indents JSON, or returns it as it came when it does not parse.
func pretty(body []byte) string {
	var b bytes.Buffer
	if err := json.Indent(&b, body, "", "  "); err != nil {
		return string(body)
	}
	return b.String()
}

// htmlStart matches the start of an HTML document.
var htmlStart = regexp.MustCompile(`(?i)^\s*(<!doctype html|<html[\s>])`)

// looksLikeHTML sniffs an unlabelled body.
func looksLikeHTML(body []byte) bool {
	return htmlStart.Match(body[:min(len(body), 512)])
}

// looksLikeJSON reports whether text is a JSON object or array. A bare
// scalar is excluded: "1" is also a text file.
func looksLikeJSON(text string) bool {
	trimmed := strings.TrimSpace(text)
	return (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed))
}
