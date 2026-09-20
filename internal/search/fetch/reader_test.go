package fetch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/search/filter"
	"github.com/erlidev/eika/internal/search/page"
	"github.com/erlidev/eika/internal/search/searchtest"
)

// keys serves a fixed GitHub token.
type keys string

func (k keys) SearchKey(_ context.Context, name string) (string, error) {
	if name == "github" {
		return string(k), nil
	}
	return "", nil
}

// quota serves a fixed GitHub limit.
type quota search.Limit

func (q quota) Limit(context.Context, string) (search.Limit, error) { return search.Limit(q), nil }

// serve answers every request with body as contentType.
func serve(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write([]byte(body))
	}
}

func newReader(handler http.HandlerFunc, cfg fetch.Config) (*fetch.Reader, *searchtest.Transport) {
	client, tr := searchtest.Client(handler)
	cfg.Client = client
	return fetch.New(cfg), tr
}

// inProcess runs filters in the test process, as eikad would in the sandbox.
func inProcess(_ context.Context, req page.FilterRequest) (page.FilterOutcome, error) {
	return filter.Run(req), nil
}

func read(t *testing.T, r *fetch.Reader, req fetch.Request) fetch.Outcome {
	t.Helper()
	out, err := r.Read(t.Context(), req, inProcess)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func htmlPage(body string) string {
	return "<!doctype html><html><head><title>T</title></head><body><main><h1>Heading</h1><p>" + body + "</p></main></body></html>"
}

var long = strings.Repeat("Body text long enough to clear the container minimum. ", 6)

func matches(t *testing.T, text string, patterns ...string) {
	t.Helper()
	for _, p := range patterns {
		if !regexp.MustCompile(p).MatchString(text) {
			t.Errorf("text does not match %q:\n%s", p, text)
		}
	}
}

func TestAnHTMLPageIsExtracted(t *testing.T) {
	r, _ := newReader(serve("text/html", htmlPage(long)), fetch.Config{})
	out := read(t, r, fetch.Request{URL: "https://example.com/doc"})
	if out.IsError || out.Details.Container != "main" || !strings.HasPrefix(out.Text, "# Heading") || out.Details.Cached {
		t.Errorf("outcome = %+v", out)
	}

	// A .md path that answers with HTML still goes through the extractor.
	r, _ = newReader(serve("text/html; charset=utf-8", htmlPage(long)), fetch.Config{})
	if out := read(t, r, fetch.Request{URL: "https://example.com/README.md"}); out.Details.Container != "main" {
		t.Errorf("container = %q", out.Details.Container)
	}
}

func TestDispatchByContentType(t *testing.T) {
	jsonFenced := "```json\n{\n  \"b\": 1,\n  \"a\": [\n    2\n  ]\n}\n```"
	cases := []struct {
		name, url, contentType, body, want string
	}{
		{"source is fenced", "https://example.com/x.py", "text/plain", "def f():\n    return 1\n", "```python\ndef f():\n    return 1\n```"},
		{"markdown passes through", "https://example.com/x.md", "text/markdown", "# Title\n\ntext\n", "# Title\n\ntext\n"},
		{"json is pretty", "https://example.com/data", "application/json", `{"b":1,"a":[2]}`, jsonFenced},
		{"json labelled text", "https://registry.npmjs.org/x/latest", "text/plain", `{"b":1,"a":[2]}`, jsonFenced},
		{"text that is not json", "https://example.com/notes.txt", "text/plain", "[draft] release notes\n", "[draft] release notes\n"},
		{"latin1", "https://example.com/x.txt", "text/plain; charset=iso-8859-1", "caf\xe9", "café"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _ := newReader(serve(c.contentType, c.body), fetch.Config{})
			if out := read(t, r, fetch.Request{URL: c.url}); out.Text != c.want || out.IsError {
				t.Errorf("text = %q, want %q", out.Text, c.want)
			}
		})
	}
}

func TestAMetaCharsetIsHonoured(t *testing.T) {
	body := `<!doctype html><html><head><meta charset="windows-1252"><title>T</title></head><body><main><h1>H</h1><p>caf` +
		"\xe9 " + strings.Repeat("body text ", 30) + "</p></main></body></html>"
	r, _ := newReader(serve("text/html", body), fetch.Config{})
	matches(t, read(t, r, fetch.Request{URL: "https://example.com/a"}).Text, "café")
}

func TestUnreadableTypesFailInOneLine(t *testing.T) {
	for contentType, want := range map[string]string{
		"application/pdf": "fetch failed: PDF is not supported; fetch the HTML version if one exists",
		"application/zip": "fetch failed: unsupported content type application/zip (2 bytes)",
	} {
		r, _ := newReader(serve(contentType, "PK"), fetch.Config{})
		if out := read(t, r, fetch.Request{URL: "https://example.com/a"}); !out.IsError || out.Text != want {
			t.Errorf("%s: %q", contentType, out.Text)
		}
	}
}

func TestRawFormatReturnsTheBody(t *testing.T) {
	r, _ := newReader(serve("text/html", htmlPage(long)), fetch.Config{})
	matches(t, read(t, r, fetch.Request{URL: "https://example.com/doc", Format: fetch.Raw}).Text, "<!doctype html>")
}

func TestRedirects(t *testing.T) {
	r, _ := newReader(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Host == "example.com" {
			http.Redirect(w, req, "https://cdn.example.com/docs/a.html", http.StatusFound)
			return
		}
		serve("text/html", `<main><h1>H</h1><p>`+long+`<a href="b.html">next</a></p></main>`)(w, req)
	}, fetch.Config{})
	out := read(t, r, fetch.Request{URL: "https://example.com/a"})
	if out.Details.FinalURL != "https://cdn.example.com/docs/a.html" {
		t.Errorf("final url = %s", out.Details.FinalURL)
	}
	matches(t, out.Text, `_Redirected to https://cdn\.example\.com/docs/a\.html_`, `\(https://cdn\.example\.com/docs/b\.html\)`)

	// A rewritten URL went where it was told, so it is no redirect.
	r, tr := newReader(serve("text/plain", "x"), fetch.Config{})
	if out := read(t, r, fetch.Request{URL: "https://github.com/a/b/blob/main/x.txt"}); out.Text != "x" {
		t.Errorf("text = %q", out.Text)
	}
	if u := tr.Requests()[0].URL.String(); u != "https://raw.githubusercontent.com/a/b/main/x.txt" {
		t.Errorf("requested %s", u)
	}
}

func TestTheBodyIsCapped(t *testing.T) {
	r, _ := newReader(serve("text/plain", strings.Repeat("x", 2_500_000)), fetch.Config{})
	out := read(t, r, fetch.Request{URL: "https://example.com/big.txt", Filter: "String(text.length)"})
	if !out.Details.BodyTruncated || !strings.HasPrefix(out.Text, "2000000") {
		t.Errorf("outcome = %q %+v", out.Text, out.Details)
	}
}

func TestBadAddressesAreRefusedBeforeAnyRequest(t *testing.T) {
	r, tr := newReader(serve("text/plain", "secret"), fetch.Config{})
	for url, want := range map[string]string{
		"http://169.254.169.254/latest/meta-data/": "refusing to fetch a private address (169.254.169.254)",
		"http://127.0.0.1:8888/search":             "refusing to fetch a private address (127.0.0.1)",
		"http://10.0.0.5/admin":                    "refusing to fetch a private address (10.0.0.5)",
		"http://[::1]/":                            "refusing to fetch a private address (::1)",
		"http://localhost/":                        "refusing to fetch a private address (localhost)",
		"http://box.local/":                        "refusing to fetch a private address (box.local)",
		"file:///etc/passwd":                       "not a valid URL: file:///etc/passwd",
		"ftp://example.com/x":                      "unsupported scheme ftp; use http or https",
	} {
		if out := read(t, r, fetch.Request{URL: url}); !out.IsError || out.Text != "fetch failed: "+want {
			t.Errorf("%s: %q", url, out.Text)
		}
	}
	if len(tr.Requests()) != 0 {
		t.Error("a refused address was requested")
	}
}

func TestRequestsAreBounded(t *testing.T) {
	r, _ := newReader(serve("text/plain", "x"), fetch.Config{})
	for _, c := range []struct {
		req  fetch.Request
		want string
	}{
		{fetch.Request{URL: " "}, "fetch failed: url is empty."},
		{fetch.Request{URL: "https://example.com/" + strings.Repeat("a", 2048)}, "fetch failed: url is longer than 2048 characters."},
		{fetch.Request{URL: "https://example.com/", Format: "pdf"}, "fetch failed: format must be markdown, text, or raw."},
		{fetch.Request{URL: "https://example.com/", Section: "x", Format: fetch.Raw}, "fetch failed: section needs markdown or text format, not raw."},
		{fetch.Request{URL: "https://example.com/", Filter: strings.Repeat("x", 10_001)}, "fetch failed: filter is longer than 10000 characters."},
	} {
		if out := read(t, r, c.req); !out.IsError || out.Text != c.want {
			t.Errorf("%+v: %q", c.req, out.Text)
		}
	}
}

func TestPagesAreCached(t *testing.T) {
	r, tr := newReader(serve("text/html", htmlPage(long)), fetch.Config{})
	first := read(t, r, fetch.Request{URL: "https://example.com/doc"})
	second := read(t, r, fetch.Request{URL: "https://example.com/doc#heading"})
	if first.Details.Cached || !second.Details.Cached || len(tr.Requests()) != 1 || r.Cached() != 1 {
		t.Errorf("first %+v, second %+v, %d requests", first.Details, second.Details, len(tr.Requests()))
	}
	raw := read(t, r, fetch.Request{URL: "https://example.com/doc", Format: fetch.Raw})
	if raw.Details.Cached || len(tr.Requests()) != 2 {
		t.Error("raw was served from the markdown entry")
	}
}

func TestFailuresAreOneLine(t *testing.T) {
	r, _ := newReader(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusNotFound) }, fetch.Config{})
	if out := read(t, r, fetch.Request{URL: "https://example.com/x"}); !out.IsError || out.Text != "fetch failed: HTTP 404" {
		t.Errorf("text = %q", out.Text)
	}
}

func TestGitHubReads(t *testing.T) {
	t.Run("a repository reads its README", func(t *testing.T) {
		r, tr := newReader(serve("text/plain", "# tokio\n\nAn async runtime."), fetch.Config{})
		out := read(t, r, fetch.Request{URL: "https://github.com/tokio-rs/tokio"})
		req := tr.Requests()[0]
		if req.URL.String() != "https://api.github.com/repos/tokio-rs/tokio/readme" || req.Header.Get("Accept") != "application/vnd.github.raw" {
			t.Errorf("request = %s %v", req.URL, req.Header)
		}
		if out.Text != "# tokio\n\nAn async runtime." {
			t.Errorf("text = %q", out.Text)
		}
	})
	t.Run("a blob reads the raw file with the token", func(t *testing.T) {
		r, tr := newReader(serve("text/plain", "fn main() {}"), fetch.Config{Keys: keys("tok")})
		out := read(t, r, fetch.Request{URL: "https://github.com/a/b/blob/main/src/main.rs"})
		req := tr.Requests()[0]
		if len(tr.Requests()) != 1 || req.URL.Host != "raw.githubusercontent.com" || req.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request = %s %v", req.URL, req.Header)
		}
		if out.Text != "```rust\nfn main() {}\n```" {
			t.Errorf("text = %q", out.Text)
		}
	})
	t.Run("the token goes to GitHub's content hosts only", func(t *testing.T) {
		for _, u := range []string{
			"https://evilgithubusercontent.com/x.txt",
			"https://githubusercontent.com.evil.example/x.txt",
			"http://raw.githubusercontent.com/a/b/main/x.txt",
		} {
			r, tr := newReader(serve("text/plain", "x"), fetch.Config{Keys: keys("tok")})
			read(t, r, fetch.Request{URL: u})
			if got := tr.Requests()[0].Header.Get("Authorization"); got != "" {
				t.Errorf("%s was sent Authorization %q", u, got)
			}
		}
	})
	t.Run("a contents URL reads the file", func(t *testing.T) {
		r, tr := newReader(serve("text/plain", "export const answer = 42;\n"), fetch.Config{})
		out := read(t, r, fetch.Request{URL: "https://api.github.com/repos/a/b/contents/src/answer.ts?ref=main"})
		if u := tr.Requests()[0].URL.String(); u != "https://api.github.com/repos/a/b/contents/src/answer.ts?ref=main" {
			t.Errorf("url = %s", u)
		}
		if out.Text != "# src/answer.ts\n\n```typescript\nexport const answer = 42;\n```" {
			t.Errorf("text = %q", out.Text)
		}
	})
	t.Run("an issue is assembled with its comments", func(t *testing.T) {
		r, tr := newReader(func(w http.ResponseWriter, req *http.Request) {
			if strings.HasSuffix(req.URL.Path, "/comments") {
				searchtest.JSON([]map[string]any{{"body": "Agreed.", "user": map[string]string{"login": "bob"}, "created_at": "2026-02-02T00:00:00Z"}})(w, req)
				return
			}
			searchtest.JSON(map[string]any{"title": "Crash on startup", "state": "open", "body": "It crashes.",
				"user": map[string]string{"login": "alice"}, "created_at": "2026-01-01T00:00:00Z"})(w, req)
		}, fetch.Config{})
		out := read(t, r, fetch.Request{URL: "https://github.com/a/b/issues/7"})
		if u := tr.Requests()[0].URL.String(); u != "https://api.github.com/repos/a/b/issues/7" {
			t.Errorf("url = %s", u)
		}
		matches(t, out.Text, `^# Crash on startup`, `a/b#7 · issue · open`, `\*\*alice\*\* · 2026-01-01`, `It crashes\.`, `\*\*bob\*\* · 2026-02-02`, `Agreed\.`)
	})
	t.Run("a pull request's files read the diff", func(t *testing.T) {
		r, tr := newReader(serve("text/plain", "--- a/x\n+++ b/x\n"), fetch.Config{})
		out := read(t, r, fetch.Request{URL: "https://github.com/a/b/pull/9/files"})
		req := tr.Requests()[0]
		if req.URL.String() != "https://api.github.com/repos/a/b/pulls/9" || req.Header.Get("Accept") != "application/vnd.github.diff" {
			t.Errorf("request = %s %v", req.URL, req.Header)
		}
		matches(t, out.Text, "^# a/b#9 diff\n\n```diff\n--- a/x")
	})
	t.Run("a tree lists directories first", func(t *testing.T) {
		r, tr := newReader(searchtest.JSON([]map[string]any{
			{"name": "util.ts", "type": "file", "size": 120},
			{"name": "nested", "type": "dir"},
			{"name": "api.ts", "type": "file", "size": 44},
		}), fetch.Config{})
		out := read(t, r, fetch.Request{URL: "https://github.com/a/b/tree/main/src"})
		if u := tr.Requests()[0].URL.String(); !strings.HasSuffix(u, "/repos/a/b/contents/src?ref=main") {
			t.Errorf("url = %s", u)
		}
		if out.Text != "# a/b tree: /src\n\n- nested/\n- api.ts (44 bytes)\n- util.ts (120 bytes)" {
			t.Errorf("text = %q", out.Text)
		}
	})
	t.Run("a release reads its notes", func(t *testing.T) {
		r, _ := newReader(searchtest.JSON(map[string]any{"name": nil, "tag_name": "v1.2.0", "published_at": "2026-03-01T00:00:00Z", "body": "Fixes."}), fetch.Config{})
		out := read(t, r, fetch.Request{URL: "https://github.com/a/b/releases/tag/v1.2.0"})
		if out.Text != "# v1.2.0\na/b · 2026-03-01\n\nFixes." {
			t.Errorf("text = %q", out.Text)
		}
	})
	t.Run("a gist reads its files", func(t *testing.T) {
		r, _ := newReader(searchtest.JSON(map[string]any{"description": "demo", "files": map[string]any{
			"b.py": map[string]any{"filename": "b.py", "language": "Python", "content": "print(1)"},
			"a.md": map[string]any{"filename": "a.md", "language": "Markdown", "content": "hi"},
		}}), fetch.Config{})
		out := read(t, r, fetch.Request{URL: "https://gist.github.com/someone/abc123"})
		if out.Text != "# demo\n\n## a.md\n\n```markdown\nhi\n```\n\n## b.py\n\n```python\nprint(1)\n```" {
			t.Errorf("text = %q", out.Text)
		}
	})
}

func TestGitHubReadsShareTheSearchBucket(t *testing.T) {
	tracker, _ := search.NewTracker(t.Context(), nil, nil)
	r, tr := newReader(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/comments") {
			searchtest.JSON([]any{})(w, req)
			return
		}
		searchtest.JSON(map[string]any{"title": "t"})(w, req)
	}, fetch.Config{Tracker: tracker, Quota: quota{Day: 1}})
	read(t, r, fetch.Request{URL: "https://github.com/a/b/issues/1"})
	if u := tracker.Usage("github", time.Now()); u.DayUsed != 1 {
		t.Errorf("an issue read counted %d uses, want 1 for two requests", u.DayUsed)
	}
	out := read(t, r, fetch.Request{URL: "https://github.com/a/b/issues/2"})
	if out.Text != "fetch failed: GitHub daily quota spent" || len(tr.Requests()) != 2 {
		t.Errorf("text = %q after %d requests", out.Text, len(tr.Requests()))
	}

	reset := time.Now().Add(time.Hour).Unix()
	limited, _ := newReader(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		w.WriteHeader(http.StatusForbidden)
	}, fetch.Config{Tracker: tracker})
	read(t, limited, fetch.Request{URL: "https://github.com/c/d"})
	if u := tracker.Usage("github", time.Now()); u.CooldownUntil.Unix() != reset {
		t.Errorf("cooldown until %s, want the reset time", u.CooldownUntil)
	}
}

// guide has one small section and one far past the budget.
var guide = "# Guide\n\nintro\n\n## Timeouts\n\nthe request timeout is 30s. " + strings.Repeat("padding ", 6000) +
	"\n\n## Task object\n\ntask body\n"

func readGuide(t *testing.T, req fetch.Request) fetch.Outcome {
	t.Helper()
	r, _ := newReader(serve("text/markdown", guide), fetch.Config{})
	req.URL = cmpOr(req.URL, "https://example.com/doc")
	return read(t, r, req)
}

func cmpOr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func TestAPageThatFitsComesBackWhole(t *testing.T) {
	r, _ := newReader(serve("text/markdown", "# Small\n\nall of it\n"), fetch.Config{})
	if out := read(t, r, fetch.Request{URL: "https://example.com/doc"}); out.Text != "# Small\n\nall of it\n" || out.Details.Mode != page.Full {
		t.Errorf("outcome = %q %+v", out.Text, out.Details)
	}
}

func TestAnOversizedPageIsOutlined(t *testing.T) {
	for _, format := range []string{fetch.Markdown, fetch.Text} {
		out := readGuide(t, fetch.Request{Format: format})
		if out.Details.Mode != page.Outline {
			t.Errorf("%s: mode = %s", format, out.Details.Mode)
		}
		matches(t, out.Text, "Page outline", "(?m)^## Timeouts$")
	}
}

func TestAnOversizedSectionIsTruncated(t *testing.T) {
	out := readGuide(t, fetch.Request{Section: "Timeouts"})
	if out.Details.Mode != page.Truncated || strings.Contains(out.Text, "Page outline") {
		t.Errorf("outcome = %+v", out.Details)
	}
	matches(t, out.Text, "^## Timeouts")
}

func TestSectionAndFilterCompose(t *testing.T) {
	out := readGuide(t, fetch.Request{Section: "Timeouts", Filter: "grep(/30s/, 0)"})
	if out.Details.SectionMatched == nil || !*out.Details.SectionMatched || strings.Contains(out.Text, "task body") {
		t.Errorf("outcome = %q %+v", out.Text, out.Details)
	}
	matches(t, out.Text, "the request timeout is 30s")
}

func TestAFilterAnswerIsBare(t *testing.T) {
	out := readGuide(t, fetch.Request{Filter: "sections.filter(s => /task/i.test(s.heading))"})
	matches(t, out.Text, "^## Task object", `\[filtered: ~\d+ of ~[\d,]+ tokens · 1 of 3 sections · \d+ lines\]$`)
	if regexp.MustCompile("(?m)^# ").MatchString(out.Text) {
		t.Errorf("something was prepended:\n%s", out.Text)
	}
}

func TestAFilterThatReturnsEverythingIsCut(t *testing.T) {
	out := readGuide(t, fetch.Request{Filter: "text"})
	if !out.Details.BudgetTruncated || strings.Contains(out.Text, "Page outline") {
		t.Errorf("outcome = %+v", out.Details)
	}
	matches(t, out.Text, `\[truncated: \d+ of ~\d+ tokens\]`)
}

func TestFilterOutcomes(t *testing.T) {
	empty := readGuide(t, fetch.Request{Filter: "grep(/nothing at all/)"})
	if empty.IsError || empty.Details.FilterOutcome != page.FilterEmpty {
		t.Errorf("empty = %+v", empty)
	}
	matches(t, empty.Text, "filter matched nothing", "Headings: Guide · Timeouts · Task object")

	broken := readGuide(t, fetch.Request{Filter: "grep(/x/"})
	if !broken.IsError {
		t.Error("a broken filter was not an error")
	}
	matches(t, broken.Text, "SyntaxError", `Bindings: text, lines\[\]`)

	raw := readGuide(t, fetch.Request{Filter: "grep(/timeout/, 0)", Format: fetch.Raw})
	if raw.IsError {
		t.Errorf("raw filter = %q", raw.Text)
	}
	matches(t, raw.Text, "the request timeout is 30s")
}

func TestAFilterThatCannotRunIsReported(t *testing.T) {
	r, _ := newReader(serve("text/markdown", guide), fetch.Config{})
	out, err := r.Read(t.Context(), fetch.Request{URL: "https://example.com/doc", Filter: "text"},
		func(context.Context, page.FilterRequest) (page.FilterOutcome, error) {
			return page.FilterOutcome{}, context.DeadlineExceeded
		})
	if err != nil || !out.IsError || !strings.HasPrefix(out.Text, "fetch failed: the filter could not run in the sandbox") {
		t.Errorf("outcome = %q, %v", out.Text, err)
	}
}

func TestTextFormat(t *testing.T) {
	out := readGuide(t, fetch.Request{Section: "Task object", Format: fetch.Text})
	if out.Details.SectionMatched == nil || !*out.Details.SectionMatched || regexp.MustCompile("(?m)^#").MatchString(out.Text) {
		t.Errorf("outcome = %q", out.Text)
	}
	matches(t, out.Text, "^Task object\n", "task body")

	fragment := readGuide(t, fetch.Request{URL: "https://example.com/doc#task-object", Format: fetch.Text})
	if strings.Contains(fragment.Text, "the request timeout") {
		t.Errorf("fragment read the whole page:\n%.200s", fragment.Text)
	}
	matches(t, fragment.Text, "task body")
}

func TestAMissingSection(t *testing.T) {
	out := readGuide(t, fetch.Request{Section: "Nonexistent"})
	if !out.IsError || out.Text != `fetch: no section matching "Nonexistent". Available: # Guide · ## Timeouts · ## Task object` {
		t.Errorf("text = %q", out.Text)
	}
	// A fragment that names nothing reads the page.
	if out := readGuide(t, fetch.Request{URL: "https://example.com/doc#footnote-3"}); out.IsError || out.Details.Mode != page.Outline {
		t.Errorf("fragment = %+v", out.Details)
	}
}

func TestDetailsAreJSON(t *testing.T) {
	out := readGuide(t, fetch.Request{Section: "Task object"})
	data, err := json.Marshal(out.Details)
	if err != nil || !strings.Contains(string(data), `"section_matched":true`) || !strings.Contains(string(data), `"mode":"full"`) {
		t.Errorf("details = %s, %v", data, err)
	}
}
