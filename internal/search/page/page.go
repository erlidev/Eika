package page

import (
	"cmp"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/erlidev/eika/internal/search"
)

// Bounds on what a read returns.
const (
	// ContentTokens is the most content one fetch returns. A section or a
	// filter narrows what is read; nothing widens it.
	ContentTokens = 10_000
	// headingLimit is how many headings a list of them names, enough to
	// steer a second fetch and no more.
	headingLimit = 20
	// headingTokens bounds each heading in a list. Generated API references
	// stuff their headings with links to tens of thousands of characters.
	headingTokens = 20
)

// Section is a heading and the lines under it, up to the next heading.
type Section struct {
	// Heading is the heading line, # marks included. It is empty for the
	// prose before the first heading.
	Heading string
	// Body is the section's text, its heading line first.
	Body string
	// Level is the heading's depth, zero for the prose before any heading.
	Level int
	// Start is the index of the section's first line in the page.
	Start int
}

var (
	fenceRail   = regexp.MustCompile("^\\s*(`{3,}|~{3,})")
	atxHeading  = regexp.MustCompile(`^(#{1,6})\s+\S`)
	headingMark = regexp.MustCompile(`^#{1,6}\s+`)
	inlineLink  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	separators  = regexp.MustCompile(`[-–—_]+`)
)

// Split cuts a page into sections on its ATX headings, ignoring any inside
// a fenced code block. Sections with no text are dropped.
func Split(markdown string) []Section {
	sections := []Section{{}}
	var bodies []*strings.Builder
	bodies = append(bodies, &strings.Builder{})
	fence := byte(0)
	for n, line := range strings.Split(markdown, "\n") {
		if m := fenceRail.FindStringSubmatch(line); m != nil {
			if fence == 0 {
				fence = m[1][0]
			} else if strings.TrimLeft(line, " \t")[0] == fence {
				fence = 0
			}
		}
		if fence == 0 {
			if m := atxHeading.FindStringSubmatch(line); m != nil {
				sections = append(sections, Section{Heading: strings.TrimSpace(line), Level: len(m[1]), Start: n})
				bodies = append(bodies, &strings.Builder{})
			}
		}
		b := bodies[len(bodies)-1]
		b.WriteString(line)
		b.WriteByte('\n')
	}
	out := sections[:0]
	for i, s := range sections {
		s.Body = bodies[i].String()
		if strings.TrimSpace(s.Body) != "" {
			out = append(out, s)
		}
	}
	return out
}

// HeadingText is a heading as it is shown: no # marks, links, emphasis, or
// code ticks, case kept. Select matches what it prints.
func HeadingText(heading string) string {
	heading = headingMark.ReplaceAllString(heading, "")
	heading = inlineLink.ReplaceAllString(heading, "$1")
	heading = strings.NewReplacer("`", "", "*", "").Replace(heading)
	return strings.Join(strings.Fields(heading), " ")
}

// comparable is a heading reduced to what matching compares: HeadingText,
// lowercased, with dashes and underscores folded to spaces so that a slug
// taken off a link matches the heading it points at.
func comparable(heading string) string {
	heading = headingMark.ReplaceAllString(heading, "")
	heading = inlineLink.ReplaceAllString(heading, "$1")
	heading = strings.NewReplacer("`", "", "*", "").Replace(heading)
	heading = separators.ReplaceAllString(heading, " ")
	return strings.ToLower(strings.Join(strings.Fields(heading), " "))
}

// HeadingList renders headings as one bounded line: the first few, each cut
// short, and how many more there are.
func HeadingList(headings []string) string {
	shown := make([]string, 0, min(len(headings), headingLimit))
	for _, h := range headings[:min(len(headings), headingLimit)] {
		h = inlineLink.ReplaceAllString(h, "$1")
		shown = append(shown, search.Truncate(strings.Join(strings.Fields(h), " "), headingTokens))
	}
	out := strings.Join(shown, " · ")
	if rest := len(headings) - len(shown); rest > 0 {
		out += fmt.Sprintf(" · +%d more", rest)
	}
	return out
}

// Pick is the result of looking for a section.
type Pick struct {
	Text  string
	Found bool
	// Available lists the headings there were, on a miss.
	Available []string
}

// Select returns the section whose heading best matches wanted, with its
// subsections. Matching widens in three steps, exact, prefix, then
// substring, because the model works from a heading it read rather than one
// it can copy exactly.
func Select(markdown, wanted string) Pick {
	var sections []Section
	for _, s := range Split(markdown) {
		if s.Heading != "" {
			sections = append(sections, s)
		}
	}
	target := comparable(wanted)
	index := -1
	if target != "" {
		for _, match := range []func(h string) bool{
			func(h string) bool { return h == target },
			func(h string) bool { return strings.HasPrefix(h, target) },
			func(h string) bool { return strings.Contains(h, target) },
		} {
			for i, s := range sections {
				if match(comparable(s.Heading)) {
					index = i
					break
				}
			}
			if index >= 0 {
				break
			}
		}
	}
	if index < 0 {
		available := make([]string, len(sections))
		for i, s := range sections {
			available[i] = s.Heading
		}
		return Pick{Available: available}
	}
	// Everything below the heading belongs to it, up to the next heading at
	// the same or a higher level.
	var b strings.Builder
	b.WriteString(sections[index].Body)
	for _, s := range sections[index+1:] {
		if s.Level <= sections[index].Level {
			break
		}
		b.WriteString(s.Body)
	}
	return Pick{Text: strings.TrimRight(b.String(), " \t\n"), Found: true}
}

// Fragment returns a URL's fragment, decoded, empty when there is none.
func Fragment(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Fragment
}

// Mode says how a read met its budget.
type Mode string

// The ways a read meets its budget.
const (
	// Full is the whole content: it fit.
	Full Mode = "full"
	// Truncated is the content cut on a section boundary, with a note of
	// what was left out.
	Truncated Mode = "truncated"
	// Outline is a map of the page's headings instead of its content.
	Outline Mode = "outline"
)

// Shaped is content fitted to a budget.
type Shaped struct {
	Text string
	Mode Mode
}

// Shape fits content to a token budget. Content that fits is returned
// whole. Past the budget the rule turns on whether the read was narrowed: a
// plain read of an oversized page gets the outline, because the head of a
// document is rarely the answer, while a section or a filter gets its own
// content cut short, because a map is no answer to a specific question.
func Shape(markdown string, tokens int, narrowed bool) Shaped {
	if len(markdown) <= tokens*search.CharsPerToken {
		return Shaped{Text: markdown, Mode: Full}
	}
	if narrowed {
		return budget(markdown, tokens)
	}
	return outline(markdown, tokens)
}

// readOne is how every note tells the model to read a section.
const readOne = `Read one with section: "<heading>", or narrow with filter:.`

// budget cuts content on a section boundary and says what was dropped, so
// the model can decide whether a narrower read is worth making.
func budget(markdown string, tokens int) Shaped {
	limit := tokens * search.CharsPerToken
	var kept strings.Builder
	var dropped []string
	for _, s := range Split(markdown) {
		// Once one section is dropped, every later one is too: keeping them
		// out of order would misrepresent the page.
		if len(dropped) == 0 && kept.Len()+len(s.Body) <= limit {
			kept.WriteString(s.Body)
			continue
		}
		dropped = append(dropped, cmp.Or(s.Heading, "(untitled section)"))
	}
	text := strings.TrimRight(kept.String(), " \t\n")
	if kept.Len() == 0 {
		// A first section larger than the whole budget still yields its
		// head, so it is not one of the sections not shown.
		text = search.Truncate(markdown, tokens)
		if len(dropped) > 0 {
			dropped = dropped[1:]
		}
	}
	notes := []string{fmt.Sprintf("\n\n[truncated: %d of ~%d tokens]",
		roundDiv(len(text), search.CharsPerToken), roundDiv(len(markdown), search.CharsPerToken))}
	if len(dropped) > 0 {
		notes = append(notes, "Sections not shown: "+HeadingList(dropped), readOne)
	} else {
		notes = append(notes, "Narrow with filter: to get the rest of this content.")
	}
	return Shaped{Text: text + strings.Join(notes, "\n"), Mode: Truncated}
}

// outline maps an oversized page: every heading with its nesting while that
// fits the budget, since the nesting tells two similar headings apart, and
// the flat, capped list past it.
func outline(markdown string, tokens int) Shaped {
	sections := Split(markdown)
	var headings []Section
	for _, s := range sections {
		if s.Heading != "" {
			headings = append(headings, s)
		}
	}
	head := fmt.Sprintf("Page outline - ~%s tokens, %s, %s. Over the %s token budget.",
		search.Thousands(roundDiv(len(markdown), search.CharsPerToken)), search.Plural(len(sections), "section"),
		search.Plural(strings.Count(markdown, "\n")+1, "line"), search.Thousands(tokens))
	foot := readOne
	if len(headings) == 0 {
		foot = "Narrow with filter: - lines.slice(0, 200) reads the head of it."
	}

	body := "This page has no headings."
	if len(headings) > 0 {
		nested := make([]string, len(headings))
		flat := make([]string, len(headings))
		for i, s := range headings {
			flat[i] = HeadingText(s.Heading)
			nested[i] = strings.Repeat("#", s.Level) + " " + search.Truncate(flat[i], headingTokens)
		}
		body = strings.Join(nested, "\n")
		if len(head)+len(body)+len(foot) > tokens*search.CharsPerToken {
			body = "Headings: " + HeadingList(flat)
		}
	}
	return Shaped{Text: strings.Join([]string{head, "", body, "", foot}, "\n"), Mode: Outline}
}

var (
	fenceLine   = regexp.MustCompile("(?m)^```.*$")
	headingLine = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	imageLink   = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	quoteMark   = regexp.MustCompile(`(?m)^>\s?`)
	blankRun    = regexp.MustCompile(`\n{3,}`)
)

// PlainText strips Markdown to prose, for a read that asked for text. It
// runs on the answer only: the headings it strips are what section
// selection, the filter, and the outline all key off.
func PlainText(markdown string) string {
	markdown = fenceLine.ReplaceAllString(markdown, "")
	markdown = headingLine.ReplaceAllString(markdown, "")
	markdown = imageLink.ReplaceAllString(markdown, "$1")
	markdown = inlineLink.ReplaceAllString(markdown, "$1")
	markdown = strings.NewReplacer("*", "", "_", "", "`", "").Replace(markdown)
	markdown = quoteMark.ReplaceAllString(markdown, "")
	markdown = blankRun.ReplaceAllString(markdown, "\n\n")
	return strings.TrimSpace(markdown)
}

// backtickRun finds runs of backticks.
var backtickRun = regexp.MustCompile("`+")

// Fence wraps body in a code fence one backtick longer than the longest run
// inside it, so no content can close it early.
func Fence(body, lang string) string {
	longest := 0
	for _, run := range backtickRun.FindAllString(body, -1) {
		longest = max(longest, len(run))
	}
	rail := strings.Repeat("`", max(3, longest+1))
	return rail + lang + "\n" + strings.TrimRight(body, "\n") + "\n" + rail
}

// roundDiv divides and rounds to the nearest integer.
func roundDiv(n, d int) int { return (n + d/2) / d }
