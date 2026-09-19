package filter_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/search/filter"
	"github.com/erlidev/eika/internal/search/page"
)

var guide = strings.Join([]string{
	"# Guide", "", "intro text", "",
	"## Timeouts", "", "the request timeout is 30s by default", "", "```js", "const timeout = 30;", "```", "",
	"## Errors", "", "a timeout raises TimeoutError", "", "```python", "raise TimeoutError()", "```", "",
	"## Task object", "", "task body",
}, "\n")

func run(source string) page.FilterOutcome {
	return filter.Run(page.FilterRequest{Markdown: guide, Source: source})
}

func ok(t *testing.T, source string) string {
	t.Helper()
	out := run(source)
	if out.Kind != page.FilterOK {
		t.Fatalf("filter %q = %s: %s", source, out.Kind, out.Text)
	}
	return out.Text
}

func failed(t *testing.T, out page.FilterOutcome) string {
	t.Helper()
	if out.Kind == page.FilterOK {
		t.Fatalf("filter succeeded: %s", out.Text)
	}
	return out.Text
}

func TestTheSandboxHasNothingToReachFor(t *testing.T) {
	for _, source := range []string{"require('node:fs')", "process.env", "fetch('http://x')", "setTimeout"} {
		if msg := failed(t, run(source)); !strings.Contains(msg, "is not defined") {
			t.Errorf("%s: %s", source, msg)
		}
	}
}

func TestARunawayIsCutOff(t *testing.T) {
	out := filter.Run(page.FilterRequest{Markdown: guide, Source: "while (true) {}", TimeoutMS: 100})
	if msg := failed(t, out); !strings.Contains(msg, "filter timed out after 100ms.") {
		t.Errorf("message = %s", msg)
	}
}

func TestFiltersRunSynchronously(t *testing.T) {
	if msg := failed(t, run("await new Promise(() => {})")); !strings.Contains(msg, "SyntaxError") {
		t.Errorf("await: %s", msg)
	}
	if msg := failed(t, run("Promise.resolve('x')")); !strings.Contains(msg, "Filters run synchronously") {
		t.Errorf("promise: %s", msg)
	}
}

func TestASyntaxErrorNamesTheBindings(t *testing.T) {
	out := run("grep(/x/")
	msg := failed(t, out)
	if out.Kind != page.FilterError || !strings.Contains(msg, "SyntaxError") || !strings.Contains(msg, "\nBindings: text, lines[]") {
		t.Errorf("message = %s", msg)
	}
}

func TestExpressionAndReturnFormsBothWork(t *testing.T) {
	for _, source := range []string{"sections[1].text", "const s = sections[1]; return s.text;"} {
		if text := ok(t, source); !strings.HasPrefix(text, "## Timeouts") {
			t.Errorf("%s = %q", source, text)
		}
	}
}

func TestABindingCannotBeReassigned(t *testing.T) {
	if text := ok(t, "lines = []; return String(lines.length);"); text != "23" {
		t.Errorf("lines.length after reassigning = %q, want 23", text)
	}
}

func TestAnOversizedReturnFailsLoudly(t *testing.T) {
	if msg := failed(t, run("'x'.repeat(5000000)")); !strings.Contains(msg, "5,000,000 characters. Select less") {
		t.Errorf("message = %s", msg)
	}
}

func TestGrep(t *testing.T) {
	text := ok(t, "grep(/30s/, 1)")
	if !regexp.MustCompile(`Timeouts · lines\[\d+\.\.\d+\]`).MatchString(text) ||
		!strings.Contains(text, "the request timeout is 30s by default") || strings.Contains(text, "task body") {
		t.Errorf("grep(/30s/, 1) =\n%s", text)
	}

	merged := ok(t, "grep(/timeout/i, 3)")
	if strings.Count(merged, "lines[") != 1 || strings.Count(merged, "the request timeout is 30s") != 1 {
		t.Errorf("overlapping hits did not merge:\n%s", merged)
	}

	if n := strings.Count(ok(t, "grep(/TIMEOUT/gi, 0)"), "lines["); n != 5 {
		t.Errorf("grep with g flag found %d runs, want 5", n)
	}
	if run("grep(/TIMEOUT/, 0)").Kind != page.FilterEmpty {
		t.Error("a case-sensitive grep matched")
	}
	if text := ok(t, "grep('TimeoutError', 0)"); !strings.Contains(text, "raise TimeoutError") {
		t.Errorf("string pattern = %s", text)
	}
	if msg := failed(t, run("grep(42)")); !strings.Contains(msg, "needs a regular expression or a string") {
		t.Errorf("bad pattern = %s", msg)
	}
}

func TestCode(t *testing.T) {
	all := ok(t, "code()")
	if !strings.Contains(all, "const timeout = 30;") || !strings.Contains(all, "raise TimeoutError()") {
		t.Errorf("code() =\n%s", all)
	}
	if js := ok(t, "code('js')"); !strings.HasPrefix(js, "```js") || strings.Contains(js, "raise") {
		t.Errorf("code('js') =\n%s", js)
	}
	nested := filter.Run(page.FilterRequest{Markdown: "# T\n\n````md\n```js\nx\n```\n````\n\n## After\n\nbody\n", Source: "code()"})
	if !strings.Contains(nested.Text, "```js\nx\n```") || strings.Contains(nested.Text, "## After") {
		t.Errorf("nested fence =\n%s", nested.Text)
	}
}

func TestRendering(t *testing.T) {
	if text := ok(t, "[sections[3], sections[1]]"); strings.Index(text, "## Timeouts") > strings.Index(text, "## Task object") {
		t.Errorf("sections out of document order:\n%s", text)
	}
	if text := ok(t, "lines.slice(0, 3)"); text != "# Guide\n\nintro text" {
		t.Errorf("lines = %q", text)
	}
	if text := ok(t, "[sections[1].text, sections[3].text]"); !strings.Contains(text, "```\n\n## Task object") {
		t.Errorf("blocks = %q", text)
	}
	if text := ok(t, "({ answer: 30 })"); text != "```json\n{\n  \"answer\": 30\n}\n```" {
		t.Errorf("object = %q", text)
	}
	for _, source := range []string{"text.length", "true", "null", "undefined"} {
		if msg := failed(t, run(source)); !strings.Contains(msg, "Return a string, a section, or an array of either") {
			t.Errorf("%s: %s", source, msg)
		}
	}
}

func TestAnEmptyAnswerMapsThePage(t *testing.T) {
	out := run("grep(/nothing at all/)")
	if out.Kind != page.FilterEmpty ||
		!regexp.MustCompile(`filter matched nothing\. Page: 4 sections, 23 lines, ~\d+ tokens\.`).MatchString(out.Text) ||
		!strings.Contains(out.Text, "Headings: Guide · Timeouts · Errors · Task object") {
		t.Errorf("outcome = %+v", out)
	}

	var b strings.Builder
	for i := range 300 {
		b.WriteString("## Section " + string(rune('0'+i%10)) + "\n\nbody\n\n")
	}
	many := filter.Run(page.FilterRequest{Markdown: b.String(), Source: "''"})
	if len(many.Text) > 1200 || !strings.Contains(many.Text, "+280 more") {
		t.Errorf("map of 300 headings (%d bytes):\n%s", len(many.Text), many.Text)
	}
}

func TestTheFooterStatesTheCoordinateSpace(t *testing.T) {
	out := run("sections[1]")
	if out.Kind != page.FilterOK || !regexp.MustCompile(`\[filtered: ~\d+ of ~\d+ tokens · 1 of 4 sections · 23 lines\]`).MatchString(out.Footer) {
		t.Errorf("outcome = %+v", out)
	}
}

func TestAnOversizedAnswerIsCutToTheBudget(t *testing.T) {
	out := filter.Run(page.FilterRequest{Markdown: guide, Source: "text.repeat(50)", Tokens: 100})
	if out.Kind != page.FilterOK || !out.Truncated || out.Footer != "" || !strings.Contains(out.Text, "[truncated:") || len(out.Text) > 1000 {
		t.Errorf("outcome = %+v", out)
	}
}

func TestServeSpeaksJSON(t *testing.T) {
	var out strings.Builder
	in := strings.NewReader(`{"markdown":"# A\n\nbody","source":"sections[0]"}`)
	if err := filter.Serve(in, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), `{"kind":"ok","text":"# A\n\nbody","footer":`) {
		t.Errorf("output = %s", out.String())
	}
	if err := filter.Serve(strings.NewReader("not json"), &out); err == nil {
		t.Error("Serve accepted a malformed request")
	}
}
