package search_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/search"
)

func TestNormalizeURLCollapsesCosmeticDifferences(t *testing.T) {
	for _, variant := range []string{
		"https://example.com/docs",
		"http://example.com/docs",
		"https://www.example.com/docs/",
		"https://EXAMPLE.com/docs",
		"https://example.com/docs?utm_source=x&utm_campaign=y",
		"https://example.com/docs?ref=hn",
	} {
		if got := search.NormalizeURL(variant); got != "example.com/docs" {
			t.Errorf("NormalizeURL(%q) = %q, want example.com/docs", variant, got)
		}
	}
}

func TestNormalizeURLKeepsMeaningfulParamsSorted(t *testing.T) {
	if got := search.NormalizeURL("https://example.com/s?b=2&a=1"); got != "example.com/s?a=1&b=2" {
		t.Errorf("NormalizeURL = %q, want example.com/s?a=1&b=2", got)
	}
	if search.NormalizeURL("https://example.com/s?q=a") == search.NormalizeURL("https://example.com/s?q=b") {
		t.Error("two different queries normalized to the same page")
	}
	if got := search.NormalizeURL("not a url"); got != "not a url" {
		t.Errorf("NormalizeURL(not a url) = %q", got)
	}
}

func TestDedupeKeepsFirstAndDropsIncomplete(t *testing.T) {
	out := search.Dedupe([]search.Result{
		{Title: "A", URL: "https://example.com/a", Description: "first"},
		{Title: "A dup", URL: "https://www.example.com/a/", Description: "second"},
		{Title: "B", URL: "https://example.com/b"},
		{Title: "", URL: "https://example.com/c"},
		{Title: "D", URL: ""},
	})
	if len(out) != 2 || out[0].Title != "A" || out[1].Title != "B" || out[0].Description != "first" {
		t.Errorf("Dedupe = %+v, want A (first) and B", out)
	}
}

func TestCleanStripsMarkupAndDecodesEntities(t *testing.T) {
	cases := map[string]string{
		`<span class="searchmatch">Tokio</span> is a &quot;runtime&quot; &amp; more`: `Tokio is a "runtime" & more`,
		"a\n\n  b\tc":        "a b c",
		"":                   "",
		"&#39;quoted&#39;":   "'quoted'",
		"non&nbsp;breaking ": "non breaking",
	}
	for in, want := range cases {
		if got := search.Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruncateCutsOnAWordBoundary(t *testing.T) {
	text := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron"
	out := search.Truncate(text, 5)
	body, ok := strings.CutSuffix(out, "…")
	if !ok || len(body) > 5*search.CharsPerToken || !strings.HasPrefix(text, body) || strings.HasSuffix(body, " ") {
		t.Errorf("Truncate = %q, want a word-boundary prefix of at most 20 bytes and an ellipsis", out)
	}
	if got := search.Truncate("short", 100); got != "short" {
		t.Errorf("Truncate(short) = %q", got)
	}
	if got := search.Truncate(strings.Repeat("a", 200), 5); got != strings.Repeat("a", 20)+"…" {
		t.Errorf("Truncate(one long word) = %q", got)
	}
	if got := search.Truncate(strings.Repeat("é", 30), 5); !strings.HasSuffix(got, "…") || strings.ContainsRune(got, '�') {
		t.Errorf("Truncate cut a rune in half: %q", got)
	}
}

func TestFormatResults(t *testing.T) {
	got := search.FormatResults([]search.Result{
		{Title: "Tokio", URL: "https://tokio.rs/", Description: "An asynchronous Rust runtime."},
		{Title: "No snippet", URL: "https://example.com/"},
	}, 100)
	want := strings.Join([]string{
		"1. Tokio", "   https://tokio.rs/", "   An asynchronous Rust runtime.", "",
		"2. No snippet", "   https://example.com/",
	}, "\n")
	if got != want {
		t.Errorf("FormatResults =\n%s\nwant\n%s", got, want)
	}
	if got := search.FormatResults(nil, 100); got != "No results." {
		t.Errorf("FormatResults(nil) = %q", got)
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "0 sections", 1: "1 section", 1234: "1,234 sections"} {
		if got := search.Plural(n, "section"); got != want {
			t.Errorf("Plural(%d) = %q, want %q", n, got, want)
		}
	}
}
