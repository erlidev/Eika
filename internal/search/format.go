package search

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Everything the model sees of a search is produced here, which makes this
// the file that controls a search's token cost.

// CharsPerToken converts a token budget to characters. It is rough, and good
// enough for a budget without shipping a tokenizer.
const CharsPerToken = 4

// trackingParam matches query parameters that only say where a link was
// clicked, which make one page look like several.
var trackingParam = regexp.MustCompile(`(?i)^(utm_|ref$|referrer$|fbclid$|gclid$|mc_[ce]id$|source$|_hs)`)

// NormalizeURL returns the identity of a page for deduplication and caching.
// It is not a display URL: the scheme, "www.", trailing slashes, and tracking
// parameters are all dropped, and the host is lowercased.
func NormalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	params := u.Query()
	for key := range params {
		if trackingParam.MatchString(key) {
			params.Del(key)
		}
	}
	out := host + strings.TrimRight(u.EscapedPath(), "/")
	if q := params.Encode(); q != "" { // Encode sorts by key
		out += "?" + q
	}
	return out
}

// Dedupe keeps the first result for each distinct page, in order, and drops
// results with no URL or no title.
func Dedupe(results []Result) []Result {
	seen := make(map[string]bool, len(results))
	out := make([]Result, 0, len(results))
	for _, r := range results {
		if r.URL == "" || r.Title == "" {
			continue
		}
		key := NormalizeURL(r.URL)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

var tag = regexp.MustCompile(`<[^>]*>`)

// Clean strips markup from a snippet, decodes its entities, and collapses its
// whitespace. Backends return snippets with markup in them: Wikipedia's
// searchmatch spans, HTML descriptions.
func Clean(text string) string {
	text = html.UnescapeString(tag.ReplaceAllString(text, ""))
	return strings.Join(strings.Fields(text), " ")
}

// Truncate cuts text to a token budget on a word boundary, so the model never
// sees a word cut in half, and marks the cut with an ellipsis.
func Truncate(text string, tokens int) string {
	limit := tokens * CharsPerToken
	if len(text) <= limit {
		return text
	}
	slice := text[:limit]
	for !utf8.ValidString(slice) {
		slice = slice[:len(slice)-1]
	}
	// A single word longer than the whole budget has no boundary to cut on.
	if cut := strings.LastIndexByte(slice, ' '); cut > limit*6/10 {
		slice = slice[:cut]
	}
	return strings.TrimRight(slice, " \t\n") + "…"
}

// Plural renders a count with its noun, since "1 sections" reads as a bug.
func Plural(n int, word string) string {
	s := Thousands(n) + " " + word
	if n != 1 {
		s += "s"
	}
	return s
}

// Thousands renders n with comma separators, as the model-facing notices do.
func Thousands(n int) string {
	if n < 0 {
		return "-" + Thousands(-n)
	}
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// FormatResults renders results as the model reads them: a numbered title,
// the URL, and a description cut to descriptionTokens.
func FormatResults(results []Result, descriptionTokens int) string {
	if len(results) == 0 {
		return "No results."
	}
	blocks := make([]string, len(results))
	for i, r := range results {
		lines := []string{fmt.Sprintf("%d. %s", i+1, Clean(r.Title)), "   " + r.URL}
		if d := Truncate(Clean(r.Description), descriptionTokens); d != "" {
			lines = append(lines, "   "+d)
		}
		blocks[i] = strings.Join(lines, "\n")
	}
	return strings.Join(blocks, "\n\n")
}
