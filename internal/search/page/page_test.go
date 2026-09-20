package page

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestBudgetCutsOnASectionBoundary(t *testing.T) {
	markdown := strings.Join([]string{
		"# Guide\n\nintro\n",
		"## Install\n\n" + strings.Repeat("i", 200) + "\n",
		"## Configuration\n\n" + strings.Repeat("c", 200) + "\n",
		"## Troubleshooting\n\n" + strings.Repeat("t", 200) + "\n",
	}, "\n")
	out := budget(markdown, 60)
	for _, want := range []string{"# Guide", "## Install", "Sections not shown: ## Configuration · ## Troubleshooting", readOne} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, out.Text)
		}
	}
	if !regexp.MustCompile(`\[truncated: \d+ of ~\d+ tokens\]`).MatchString(out.Text) || strings.Contains(out.Text, "ccc") {
		t.Errorf("text:\n%s", out.Text)
	}
}

func TestAHeadingInACodeFenceIsNotABoundary(t *testing.T) {
	markdown := "# Real\n\n" + strings.Repeat("x", 300) + "\n\n```sh\n# not a heading\n```\n\n## Also real\n\nbody\n"
	out := budget(markdown, 40)
	if !regexp.MustCompile(`(?m)Sections not shown: ## Also real$`).MatchString(out.Text) || strings.Contains(out.Text, "not a heading") {
		t.Errorf("text:\n%s", out.Text)
	}
	if p := Select("# Real\n\nbody\n\n```sh\n## Fake heading\n```\n", "Fake heading"); p.Found || !slices.Equal(p.Available, []string{"# Real"}) {
		t.Errorf("Select = %+v, want a miss listing only the real heading", p)
	}
}

// reference is a generated API reference: hundreds of link-stuffed headings.
func reference() string {
	var b strings.Builder
	b.WriteString("# Reference\n\n" + strings.Repeat("x", 400) + "\n\n")
	for i := range 400 {
		fmt.Fprintf(&b, "## impl [Serialize](https://docs.rs/x/trait.Serialize.html \"trait x::Serialize\") for T%d\n\nbody\n\n", i)
	}
	return b.String()
}

func TestNoticesStayBounded(t *testing.T) {
	out := budget(reference(), 100)
	notice := out.Text[strings.Index(out.Text, "Sections not shown:"):]
	if len(notice) > 1200 || !regexp.MustCompile(`(?m)\+380 more$`).MatchString(notice) || strings.Contains(notice, "https://") {
		t.Errorf("budget notice (%d bytes):\n%s", len(notice), notice)
	}
	if len(out.Text) > 100*4+1400 {
		t.Errorf("budget overran: %d bytes", len(out.Text))
	}

	o := outline(reference(), 100)
	if len(o.Text) > 100*4+400 || !strings.Contains(o.Text, "+381 more") || strings.Contains(o.Text, "https://") {
		t.Errorf("outline (%d bytes):\n%s", len(o.Text), o.Text)
	}
}

const guide = "# Guide\n\nintro\n\n## Timeouts\n\ntimeout body\n\n### Nested detail\n\nnested body\n\n" +
	"## Task object\n\ntask body\n\n## Introspection\n\nintrospection body\n"

func TestSelectReturnsTheSectionWithItsSubsections(t *testing.T) {
	p := Select(guide, "Timeouts")
	if !p.Found || !strings.HasPrefix(p.Text, "## Timeouts") || !strings.Contains(p.Text, "nested body") ||
		strings.Contains(p.Text, "task body") || strings.Contains(p.Text, "intro\n") {
		t.Errorf("Select(Timeouts) = %+v", p)
	}
	if p := Select(guide, "Nested detail"); !strings.HasPrefix(p.Text, "### Nested detail") || strings.Contains(p.Text, "task body") {
		t.Errorf("Select(Nested detail) = %+v", p)
	}
}

func TestSelectMatchesLoosely(t *testing.T) {
	for _, wanted := range []string{"Task object", "task object", "TASK OBJECT", "task-object", "task_object", "## Task object"} {
		if p := Select(guide, wanted); !strings.Contains(p.Text, "task body") {
			t.Errorf("Select(%q) missed", wanted)
		}
	}
	if p := Select(guide, "Introspect"); !strings.Contains(p.Text, "introspection body") {
		t.Error("a partial heading missed")
	}
	if p := Select(guide, "Nested"); !strings.Contains(p.Text, "nested body") {
		t.Error("a prefix missed")
	}
}

func TestSelectMissListsTheHeadings(t *testing.T) {
	p := Select(guide, "Nonexistent")
	want := []string{"# Guide", "## Timeouts", "### Nested detail", "## Task object", "## Introspection"}
	if p.Found || p.Text != "" || !slices.Equal(p.Available, want) {
		t.Errorf("Select = %+v", p)
	}
	if p := Select("just prose, no headings at all", "Anything"); p.Found || len(p.Available) != 0 {
		t.Errorf("Select on a page with no headings = %+v", p)
	}
	if p := Select(guide, "  "); p.Found {
		t.Error("an empty heading matched")
	}
}

func TestFragment(t *testing.T) {
	for in, want := range map[string]string{
		"https://x.com/a#task%20object": "task object",
		"https://x.com/a#task-object":   "task-object",
		"https://x.com/a":               "",
		"::":                            "",
	} {
		if got := Fragment(in); got != want {
			t.Errorf("Fragment(%q) = %q, want %q", in, got, want)
		}
	}
}

var big = strings.Join([]string{
	"# Coroutines\n\nintro\n",
	"## Awaitables\n\n" + strings.Repeat("a", 400) + "\n",
	"## Creating Tasks\n\n" + strings.Repeat("c", 400) + "\n",
	"### Task object\n\n" + strings.Repeat("t", 400) + "\n",
	"## Timeouts\n\n" + strings.Repeat("m", 400) + "\n",
}, "\n")

func TestShapeReturnsWhatFits(t *testing.T) {
	for _, narrowed := range []bool{true, false} {
		if out := Shape("# A\n\nshort\n", 1000, narrowed); out != (Shaped{Text: "# A\n\nshort\n", Mode: Full}) {
			t.Errorf("Shape(narrowed=%v) = %+v", narrowed, out)
		}
	}
}

func TestShapeOutlinesAnOversizedPage(t *testing.T) {
	out := Shape(big, 100, false)
	if out.Mode != Outline ||
		!regexp.MustCompile(`^Page outline - ~\d+ tokens, 5 sections, \d+ lines\. Over the 100 token budget\.`).MatchString(out.Text) ||
		!regexp.MustCompile(`(?m)^# Coroutines$`).MatchString(out.Text) ||
		!regexp.MustCompile(`(?m)^### Task object$`).MatchString(out.Text) ||
		strings.Contains(out.Text, "aaaa") || !strings.HasSuffix(out.Text, readOne) {
		t.Errorf("Shape = %+v", out)
	}
}

func TestShapeTruncatesANarrowedRead(t *testing.T) {
	out := Shape(big, 100, true)
	if out.Mode != Truncated || !strings.HasPrefix(out.Text, "# Coroutines\n\nintro") ||
		!strings.Contains(out.Text, "Sections not shown: ## Awaitables") || strings.Contains(out.Text, "Page outline") {
		t.Errorf("Shape = %+v", out)
	}
}

func TestOutlineHeadingsRoundTripAsSections(t *testing.T) {
	nested, flat := outline(big, 100).Text, outline(big, 30).Text
	if !strings.Contains(flat, "Headings: Coroutines · Awaitables") || regexp.MustCompile(`(?m)^## Awaitables$`).MatchString(flat) {
		t.Errorf("flat outline:\n%s", flat)
	}
	for _, h := range []string{"Creating Tasks", "Task object"} {
		if !strings.Contains(nested, h) || !strings.Contains(flat, h) || !Select(big, h).Found {
			t.Errorf("%q does not round-trip", h)
		}
	}
}

func TestOutlineOfAPageWithNoHeadings(t *testing.T) {
	out := outline(strings.Repeat("just prose ", 200), 20).Text
	if !strings.Contains(out, "This page has no headings.") || strings.Contains(out, `section: "<heading>"`) || !strings.Contains(out, "lines.slice") {
		t.Errorf("outline:\n%s", out)
	}
	if out := outline("# One\n\n"+strings.Repeat("x", 400), 20).Text; !regexp.MustCompile(`1 section, \d+ lines\.`).MatchString(out) {
		t.Errorf("outline:\n%s", out)
	}
}

func TestBudgetOfOneOversizedSection(t *testing.T) {
	out := budget("# Only\n\n"+strings.Repeat("w ", 2000), 50)
	if out.Mode != Truncated || !strings.HasPrefix(out.Text, "# Only") || len(out.Text) > 1000 ||
		strings.Contains(out.Text, "Sections not shown") {
		t.Errorf("budget = %+v", out)
	}
}

func TestPlainText(t *testing.T) {
	in := "# Title\n\nSome **bold** and _em_ with [a link](https://x) and ![alt](i.png).\n\n```go\ncode()\n```\n\n> quoted"
	want := "Title\n\nSome bold and em with a link and alt.\n\ncode()\n\nquoted"
	if got := PlainText(in); got != want {
		t.Errorf("PlainText =\n%q\nwant\n%q", got, want)
	}
}

func TestFenceOutrunsTheLongestBacktickRun(t *testing.T) {
	if got := Fence("a ```` b\n\n", "md"); got != "`````md\na ```` b\n`````" {
		t.Errorf("Fence = %q", got)
	}
	if got := Fence("x", ""); got != "```\nx\n```" {
		t.Errorf("Fence = %q", got)
	}
}

func TestHeadingList(t *testing.T) {
	if got := HeadingList([]string{"## [A](https://x) b", "C"}); got != "## A b · C" {
		t.Errorf("HeadingList = %q", got)
	}
}
