package fetch

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Extraction has three stages: pick the element that holds the content,
// strip the furniture out of it, then convert it. The first does most of the
// work. Documentation generators all mark their content container, and
// matching those markers is more faithful than any density heuristic, which
// is why readability is the fallback rather than the rule.

// minChars is the least text a container must hold, and render to, to be
// taken as the content rather than as chrome.
const minChars = 200

// container finds one kind of content container.
type container struct {
	label string
	find  func(doc *html.Node) *html.Node
}

// containers are tried in order, most specific first: a generator's own
// marker beats a generic semantic element.
var containers = []container{
	{"docusaurus", byClass("theme-doc-markdown")},
	{"mkdocs-material", func(doc *html.Node) *html.Node {
		var found *html.Node
		walk(doc, func(n *html.Node) bool {
			if found == nil && hasClass(n, "md-content") {
				found = findFirst(n, isAtom(atom.Article))
			}
			return found == nil
		})
		return found
	}},
	{"pydata-sphinx", byClass("bd-article")},
	{"readthedocs", byClass("rst-content")},
	{"vitepress", byClass("vp-doc")},
	{"mintlify", byID("content-area")},
	{"furo", byID("furo-main-content")},
	{"rustdoc", byID("main-content")},
	{"markdown-body", byClass("markdown-body")},
	{"article", func(doc *html.Node) *html.Node { return findFirst(doc, isAtom(atom.Article)) }},
	{"main", func(doc *html.Node) *html.Node { return findFirst(doc, isAtom(atom.Main)) }},
	{"role-main", func(doc *html.Node) *html.Node {
		return findFirst(doc, func(n *html.Node) bool { return attr(n, "role") == "main" })
	}},
}

// extracted is a page's content as Markdown.
type extracted struct {
	title    string
	markdown string
	// container names what the content was found in: a generator, a
	// semantic element, "readability", or "body". It is what to check when
	// a site extracts badly.
	container string
}

// extract finds an HTML page's content and converts it to Markdown. The
// body is decoded by its declared charset, or the one its meta tag names,
// and links resolve against base.
func extract(body []byte, contentType string, base *url.URL) (extracted, error) {
	source, err := decode(body, contentType)
	if err != nil {
		return extracted{}, err
	}
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return extracted{}, fmt.Errorf("parse page: %w", err)
	}
	if href := attr(findFirst(doc, isAtom(atom.Base)), "href"); href != "" && base != nil {
		if ref, err := url.Parse(href); err == nil {
			base = base.ResolveReference(ref)
		}
	}

	title := collapse(textOf(findFirst(doc, isAtom(atom.Title))))
	if title == "" {
		title = collapse(textOf(findFirst(doc, isAtom(atom.H1))))
	}

	for _, c := range containers {
		el := c.find(doc)
		// Measured before sanitizing, so a rejected candidate is weighed on
		// what it held.
		if el == nil || utf8.RuneCountInString(strings.TrimSpace(textOf(el))) < minChars {
			continue
		}
		if md := render(el, base); utf8.RuneCountInString(md) >= minChars {
			return extracted{title: title, markdown: md, container: c.label}, nil
		}
	}

	// Readability prunes as it scores, so it gets a parse of its own rather
	// than the document the containers were tried on.
	if fresh, err := html.Parse(strings.NewReader(source)); err == nil {
		if el := readability(fresh); el != nil {
			if md := render(el, base); utf8.RuneCountInString(md) >= minChars {
				return extracted{title: title, markdown: md, container: "readability"}, nil
			}
		}
	}

	out := extracted{title: title, container: "body"}
	if b := findFirst(doc, isAtom(atom.Body)); b != nil {
		out.markdown = render(b, base)
	}
	return out, nil
}

// render sanitizes an element and converts it to tidy Markdown.
func render(el *html.Node, base *url.URL) string {
	sanitize(el)
	return tidy(newConverter(base).render(el))
}

// dropped never carries prose, and the text of a script or a style would
// otherwise land in the output.
var dropped = []atom.Atom{
	atom.Script, atom.Style, atom.Noscript, atom.Svg, atom.Iframe, atom.Form, atom.Button,
	atom.Input, atom.Select, atom.Textarea, atom.Template, atom.Object, atom.Embed, atom.Canvas,
	atom.Dialog, atom.Link, atom.Meta,
}

// chromeRoles are the ARIA roles of page furniture.
var chromeRoles = []string{"navigation", "banner", "contentinfo", "complementary", "search"}

// permalinkClasses mark the permalink documentation generators append to
// every heading.
var permalinkClasses = []string{"headerlink", "anchor", "hash-link", "header-anchor", "anchorjs-link"}

// mediawikiClasses mark MediaWiki furniture no generic rule catches: the
// edit link on every heading, and the maintenance boxes that are notices
// about the article rather than part of it.
var mediawikiClasses = []string{"mw-editsection", "ambox", "mw-message-box", "navbox"}

// gutterClasses mark the line-number columns of highlighted code.
var gutterClasses = []string{"linenos", "lineno", "line-numbers-rows", "rouge-gutter"}

// sanitize removes everything below el that is not content: furniture,
// hidden elements, permalinks, line-number gutters, and links that carry
// nothing once the page is Markdown. Chrome goes even inside the container,
// since sidebars nested in <main> are common.
func sanitize(el *html.Node) {
	removeWhere(el, func(n *html.Node) bool {
		switch {
		case slices.Contains(dropped, n.DataAtom),
			slices.Contains([]atom.Atom{atom.Nav, atom.Header, atom.Footer, atom.Aside}, n.DataAtom),
			slices.Contains(chromeRoles, attr(n, "role")),
			attr(n, "aria-hidden") == "true", hasAttr(n, "hidden"),
			n.DataAtom == atom.A && slices.ContainsFunc(permalinkClasses, func(c string) bool { return hasClass(n, c) }),
			hasClass(n, "headerlink"),
			slices.ContainsFunc(mediawikiClasses, func(c string) bool { return hasClass(n, c) }),
			hasClass(n, "metadata") && hasClass(n, "plainlinks"),
			attr(n, "id") == "toc",
			slices.ContainsFunc(gutterClasses, func(c string) bool { return hasClass(n, c) }):
			return true
		}
		return false
	})
	unwrapLineNumbers(el)
	dropEmptyLinks(el)
}

// removeWhere removes every comment below n and every element drop accepts,
// with what it holds.
func removeWhere(n *html.Node, drop func(*html.Node) bool) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.CommentNode, c.Type == html.ElementNode && drop(c):
			n.RemoveChild(c)
		case c.Type == html.ElementNode:
			removeWhere(c, drop)
		}
		c = next
	}
}

// unwrapLineNumbers replaces the two-column tables Pygments and Rouge render
// code as with the code column alone. Left in, the line numbers would be
// interleaved into the code.
func unwrapLineNumbers(el *html.Node) {
	var tables []*html.Node
	walk(el, func(n *html.Node) bool {
		if n.DataAtom == atom.Table && (hasClass(n, "highlighttable") || hasClass(n, "rouge-table") || hasClass(n, "highlight")) {
			tables = append(tables, n)
			return false
		}
		return true
	})
	for _, t := range tables {
		code := findFirst(t, func(n *html.Node) bool {
			return n.DataAtom == atom.Td && (hasClass(n, "code") || hasClass(n, "rouge-code"))
		})
		if code != nil {
			unwrap(t, code)
		}
	}
}

// permalinkGlyph is a link that is only a permalink symbol. An anchor that
// reads "#1234" is a real link to an issue, so a word is never one.
var permalinkGlyph = regexp.MustCompile(`^[¶§#⚓🔗]+$`)

// dropEmptyLinks removes links that carry nothing once the page is Markdown:
// a permalink glyph, an icon link whose icon was dropped, and a heading's
// link to itself, which is unwrapped to its text.
func dropEmptyLinks(el *html.Node) {
	var anchors []*html.Node
	walk(el, func(n *html.Node) bool {
		if n.DataAtom == atom.A {
			anchors = append(anchors, n)
		}
		return true
	})
	for _, a := range anchors {
		if a.Parent == nil {
			continue
		}
		text := strings.TrimSpace(textOf(a))
		fragment := strings.HasPrefix(attr(a, "href"), "#")
		switch {
		case fragment && permalinkGlyph.MatchString(text):
			a.Parent.RemoveChild(a)
		case text == "" && findFirst(a, isAtom(atom.Img)) == nil:
			a.Parent.RemoveChild(a)
		case fragment && inHeading(a):
			unwrap(a, a)
		}
	}
}

// inHeading reports whether n is inside a heading.
func inHeading(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		switch p.DataAtom {
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			return true
		}
	}
	return false
}

// unwrap replaces target with the children of from.
func unwrap(target, from *html.Node) {
	parent := target.Parent
	if parent == nil {
		return
	}
	for c := from.FirstChild; c != nil; {
		next := c.NextSibling
		from.RemoveChild(c)
		parent.InsertBefore(c, target)
		c = next
	}
	parent.RemoveChild(target)
}

// hasClass reports whether an element carries a class.
func hasClass(n *html.Node, class string) bool {
	return slices.Contains(strings.Fields(attr(n, "class")), class)
}

// byClass finds the first element carrying a class.
func byClass(class string) func(*html.Node) *html.Node {
	return func(doc *html.Node) *html.Node {
		return findFirst(doc, func(n *html.Node) bool { return hasClass(n, class) })
	}
}

// byID finds the element with an id.
func byID(id string) func(*html.Node) *html.Node {
	return func(doc *html.Node) *html.Node {
		return findFirst(doc, func(n *html.Node) bool { return attr(n, "id") == id })
	}
}

var (
	trailingSpace = regexp.MustCompile(`(?m)[ \t]+$`)
	blankLines    = regexp.MustCompile(`\n{3,}`)
)

// tidy removes the blank runs and trailing spaces conversion leaves behind.
func tidy(markdown string) string {
	markdown = strings.ReplaceAll(markdown, " ", " ")
	markdown = trailingSpace.ReplaceAllString(markdown, "")
	markdown = blankLines.ReplaceAllString(markdown, "\n\n")
	return strings.TrimSpace(markdown)
}
