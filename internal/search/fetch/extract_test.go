package fetch

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

func extractFixture(t *testing.T, name, base string) extracted {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name + ".html")
	if err != nil {
		t.Fatal(err)
	}
	return extractString(t, string(body), base)
}

func extractString(t *testing.T, body, base string) extracted {
	t.Helper()
	u, _ := url.Parse(base)
	out, err := extract([]byte(body), "text/html; charset=utf-8", u)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// expect checks markdown against patterns that must and must not match.
func expect(t *testing.T, markdown string, must, mustNot []string) {
	t.Helper()
	for _, p := range must {
		if !regexp.MustCompile(p).MatchString(markdown) {
			t.Errorf("markdown does not match %q:\n%s", p, markdown)
		}
	}
	for _, p := range mustNot {
		if regexp.MustCompile(p).MatchString(markdown) {
			t.Errorf("markdown matches %q:\n%s", p, markdown)
		}
	}
}

func TestDocusaurus(t *testing.T) {
	out := extractFixture(t, "docusaurus", "https://example.com/docs/routing")
	if out.container != "docusaurus" || out.title != "Routing | Example Docs" {
		t.Errorf("container %q, title %q", out.container, out.title)
	}
	expect(t, out.markdown, []string{
		"^# Routing",
		"```typescript\nrouter\\.get\\(\"/users/:id\", getUser\\);\n```",
		`\| Option \| Type \| Default \|`,
		"\\| `caseSensitive` \\| boolean \\| `false` \\|",
		// routing is a document, so ../api/router is relative to /docs/.
		`\[router API\]\(https://example\.com/api/router\)`,
	}, []string{"Introduction", "Copyright", "__DOCUSAURUS", `\]\(#routing\)`})
}

func TestMkdocsDropsTheLineNumbers(t *testing.T) {
	out := extractFixture(t, "mkdocs", "https://example.com/config/")
	if out.container != "mkdocs-material" {
		t.Errorf("container = %q", out.container)
	}
	expect(t, out.markdown, []string{"```yaml\nretries: 3\ntimeout: 30\n```"}, []string{`(?m)^\s*1\s*$`, "<table|<pre|<div", "¶"})
}

func TestSphinx(t *testing.T) {
	out := extractFixture(t, "sphinx", "https://docs.example.com/library/asyncio.html")
	if out.container != "pydata-sphinx" {
		t.Errorf("container = %q", out.container)
	}
	expect(t, out.markdown, []string{
		"^# asyncio", "async/await syntax",
		"```python3\nasync def main\\(\\):\n {4}await asyncio\\.sleep\\(1\\)\n```",
	}, []string{"genindex|Documentation<|¶"})
}

func TestReadabilityIsTheFallback(t *testing.T) {
	out := extractFixture(t, "nav-heavy", "https://blog.example.com/caching")
	if out.container != "readability" {
		t.Errorf("container = %q", out.container)
	}
	expect(t, out.markdown, []string{"Cache invalidation is famously"}, []string{"Link two"})
}

func main(body string) string {
	return "<main><h1>T</h1><p>" + strings.Repeat("body text ", 30) + "</p>" + body + "</main>"
}

func TestSanitizing(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		must, mustNot []string
	}{
		{"data images become their alt text", `<img src="data:image/png;base64,AAAAAAAA" alt="architecture diagram">`,
			[]string{"architecture diagram"}, []string{"base64|data:image"}},
		{"a table with no header row is still a table", `<table><tr><td>alpha</td><td>beta</td></tr><tr><td>1</td><td>2</td></tr></table>`,
			[]string{`\| alpha \| beta \|\n\| --- \| --- \|\n\| 1 \| 2 \|`}, []string{"<table"}},
		{"a code block in a cell is flattened", `<table><tr><th>a</th></tr><tr><td><pre>x
y</pre><p>one</p><br>two</td></tr></table>`,
			[]string{"\\| `x y` one two \\|"}, nil},
		{"javascript links keep their text", `<p><a href="javascript:alert(1)">click</a></p>`,
			[]string{"click"}, []string{"javascript:"}},
		{"in-page links stay short", `<p>see <a href="#usage">usage</a></p>`,
			[]string{`\[usage\]\(#usage\)`}, nil},
		{"a heading's link to itself is unwrapped", `<h2><a href="#x">Usage</a><a class="anchor" href="#x">#</a></h2>`,
			[]string{"(?m)^## Usage$"}, nil},
		{"icon links whose icon was dropped go too", `<p>edit <a href="/edit"><svg></svg></a></p>`,
			nil, []string{`\]\(`}},
		{"chrome inside the container goes", `<nav>Home Docs</nav><aside>Related</aside><div role="navigation">Menu</div><div hidden>Hidden</div><div aria-hidden="true">Aria</div>`,
			nil, []string{"Home Docs|Related|Menu|Hidden|Aria"}},
		{"MediaWiki furniture goes", `<h2>History<span class="mw-editsection">[edit]</span></h2><div class="ambox">This article needs citations</div><div id="toc">Contents</div>`,
			[]string{"(?m)^## History$"}, []string{`\[edit\]|citations|Contents`}},
		{"emphasis uses underscores", `<p><em>very</em> <strong>much</strong></p>`,
			[]string{"_very_ \\*\\*much\\*\\*"}, nil},
		{"an unhighlighted block has no language", `<pre><code class="language-text">plain</code></pre>`,
			[]string{"```\nplain\n```"}, nil},
		{"a bare language class names the language", `<pre class="rust"><code>fn main() {}</code></pre>`,
			[]string{"```rust\nfn main\\(\\) \\{\\}\n```"}, nil},
		{"data-lang names the language", `<div data-lang="go"><pre><code>x := 1</code></pre></div>`,
			[]string{"```go\nx := 1\n```"}, nil},
		{"a fence outruns backticks in the code", "<pre>a ``` b</pre>",
			[]string{"````\na ``` b\n````"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expect(t, extractString(t, main(c.body), "https://example.com/").markdown, c.must, c.mustNot)
		})
	}
}

func TestAnEmptyDocumentIsEmpty(t *testing.T) {
	out := extractString(t, "", "https://example.com/")
	if out.markdown != "" || out.container != "body" {
		t.Errorf("extract(empty) = %+v", out)
	}
}

func TestTheTitleFallsBackToTheFirstHeading(t *testing.T) {
	if out := extractString(t, main(""), "https://example.com/"); out.title != "T" {
		t.Errorf("title = %q", out.title)
	}
}
