package fetch

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The readability heuristic follows Mozilla's Readability in miniature: drop
// the page furniture, prefer the element the page marks as its content, and
// otherwise score the elements that hold paragraphs of prose.
const (
	// minArticleText is the least text an <article> must hold to be taken
	// as the page's content.
	minArticleText = 250
	// minParagraph is the least text a paragraph must hold to count towards
	// its container's score.
	minParagraph = 25
)

// junk are elements that never hold what a reader of the page wants.
var junk = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true,
	atom.Svg: true, atom.Canvas: true, atom.Iframe: true, atom.Object: true, atom.Embed: true,
	atom.Video: true, atom.Audio: true, atom.Source: true, atom.Track: true, atom.Map: true,
	atom.Button: true, atom.Input: true, atom.Select: true, atom.Textarea: true,
	atom.Option: true, atom.Datalist: true, atom.Meter: true, atom.Progress: true,
	atom.Nav: true, atom.Aside: true, atom.Footer: true, atom.Dialog: true,
	atom.Link: true, atom.Meta: true,
}

// junkRoles are the ARIA roles of page furniture.
var junkRoles = map[string]bool{
	"navigation": true, "banner": true, "contentinfo": true, "complementary": true,
	"dialog": true, "alertdialog": true, "menu": true, "menubar": true, "search": true,
	"toolbar": true,
}

var (
	// unlikely matches the class or id of page furniture, unless maybe
	// matches it too.
	unlikely = regexp.MustCompile(`(?i)-ad-|ai2html|banner|breadcrumb|combx|comment|community|cookie|cover-wrap|disqus|extra|footer|gdpr|header|legends|menu|newsletter|related|remark|replies|rss|share|shoutbox|sidebar|skyscraper|social|sponsor|subscribe|supplemental|ad-break|agegate|pagination|pager|popup|yom-remote`)
	maybe    = regexp.MustCompile(`(?i)and|article|body|column|content|main|shadow`)
	// positive and negative weigh a candidate by its class and id.
	positive = regexp.MustCompile(`(?i)article|body|content|entry|hentry|h-entry|main|page|post|text|blog|story`)
	negative = regexp.MustCompile(`(?i)-ad-|hidden|banner|combx|comment|com-|contact|foot|gdpr|masthead|media|meta|outbrain|promo|related|scroll|share|shoutbox|sidebar|skyscraper|sponsor|shopping|tags|tool|widget`)
)

// readability picks the main content of a page that marks no container a
// documentation generator would: the one article, else <main>, else the
// element whose children hold the most prose. It prunes the furniture from
// doc as it goes, so it is given a parse of its own.
func readability(doc *html.Node) *html.Node {
	body := findFirst(doc, isAtom(atom.Body))
	if body == nil {
		body = doc
	}
	prune(body, false)
	return mainContent(body)
}

// prune removes page furniture, comments, and hidden elements below n.
// inContent is true inside an <article> or <main>, where a <header> holds
// the article's own title rather than the site's.
func prune(n *html.Node, inContent bool) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.CommentNode:
			n.RemoveChild(c)
		case c.Type == html.ElementNode && isFurniture(c, inContent):
			n.RemoveChild(c)
		case c.Type == html.ElementNode:
			prune(c, inContent || c.DataAtom == atom.Article || c.DataAtom == atom.Main)
		}
		c = next
	}
}

// isFurniture reports whether an element is part of the page rather than of
// its content.
func isFurniture(n *html.Node, inContent bool) bool {
	if junk[n.DataAtom] || (n.DataAtom == atom.Header && !inContent) || hidden(n) || junkRoles[attr(n, "role")] {
		return true
	}
	switch n.DataAtom {
	case atom.Body, atom.Article, atom.Main, atom.Pre, atom.Code, atom.Table, atom.A:
		return false
	}
	marker := attr(n, "class") + " " + attr(n, "id")
	return unlikely.MatchString(marker) && !maybe.MatchString(marker)
}

// hidden reports whether an element is not shown.
func hidden(n *html.Node) bool {
	if hasAttr(n, "hidden") || attr(n, "aria-hidden") == "true" {
		return true
	}
	style := strings.ReplaceAll(strings.ToLower(attr(n, "style")), " ", "")
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
}

// mainContent picks the element holding the page's content: the one article,
// else <main>, else the best scored paragraph container, else the body.
func mainContent(body *html.Node) *html.Node {
	var articles []*html.Node
	walk(body, func(n *html.Node) bool {
		if n.DataAtom == atom.Article {
			articles = append(articles, n)
			return false // an article inside an article is part of it
		}
		return true
	})
	if len(articles) == 1 && len(collapse(textOf(articles[0]))) >= minArticleText {
		return articles[0]
	}
	if m := findFirst(body, func(n *html.Node) bool {
		return n.DataAtom == atom.Main || attr(n, "role") == "main"
	}); m != nil {
		return m
	}
	if c := bestCandidate(body); c != nil {
		return c
	}
	return body
}

// bestCandidate scores every element by the prose its children and
// grandchildren hold, discounted by how much of its text is links.
func bestCandidate(body *html.Node) *html.Node {
	scores := map[*html.Node]float64{}
	var order []*html.Node
	add := func(n *html.Node, s float64) {
		if n == nil || n.Type != html.ElementNode {
			return
		}
		if _, ok := scores[n]; !ok {
			scores[n] = initialScore(n)
			order = append(order, n)
		}
		scores[n] += s
	}
	walk(body, func(n *html.Node) bool {
		if !isParagraph(n) {
			return true
		}
		text := collapse(textOf(n))
		if len(text) < minParagraph {
			return false
		}
		s := 1 + float64(strings.Count(text, ",")) + min(float64(len(text))/100, 3)
		add(n.Parent, s)
		if n.Parent != nil {
			add(n.Parent.Parent, s/2)
		}
		return false
	})
	var best *html.Node
	bestScore := 0.0
	for _, n := range order {
		if s := scores[n] * (1 - linkDensity(n)); s > bestScore {
			best, bestScore = n, s
		}
	}
	return best
}

// isParagraph reports whether an element holds prose: a paragraph, a code
// block, a table cell, or a div with no block inside it.
func isParagraph(n *html.Node) bool {
	switch n.DataAtom {
	case atom.P, atom.Pre, atom.Td:
		return true
	case atom.Div:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && blockAtoms[c.DataAtom] {
				return false
			}
		}
		return true
	}
	return false
}

// initialScore weighs a candidate by what kind of element it is and what
// its class and id say it is.
func initialScore(n *html.Node) float64 {
	s := 0.0
	switch n.DataAtom {
	case atom.Div, atom.Article, atom.Section:
		s = 5
	case atom.Pre, atom.Td, atom.Blockquote:
		s = 3
	case atom.Form, atom.Ol, atom.Ul, atom.Dl, atom.Dd, atom.Dt, atom.Li, atom.Address:
		s = -3
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Th:
		s = -5
	}
	marker := attr(n, "class") + " " + attr(n, "id")
	if positive.MatchString(marker) {
		s += 25
	}
	if negative.MatchString(marker) {
		s -= 25
	}
	return s
}

// linkDensity is the share of an element's text that is link text.
func linkDensity(n *html.Node) float64 {
	total := len(collapse(textOf(n)))
	if total == 0 {
		return 0
	}
	links := 0
	walk(n, func(c *html.Node) bool {
		if c.DataAtom == atom.A {
			links += len(collapse(textOf(c)))
			return false
		}
		return true
	})
	return float64(links) / float64(total)
}

// walk visits the elements below n in document order. visit returns whether
// to descend into the element it was given.
func walk(n *html.Node, visit func(*html.Node) bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && visit(c) {
			walk(c, visit)
		}
	}
}

// findFirst returns the first element below n that match accepts, or nil.
func findFirst(n *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(n, func(c *html.Node) bool {
		if found != nil {
			return false
		}
		if match(c) {
			found = c
			return false
		}
		return true
	})
	return found
}

// isAtom matches elements of one kind.
func isAtom(a atom.Atom) func(*html.Node) bool {
	return func(n *html.Node) bool { return n.DataAtom == a }
}

// attr returns an attribute's value, empty when n is nil or lacks it.
func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

// hasAttr reports whether n carries an attribute, whatever its value.
func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return true
		}
	}
	return false
}

// textOf returns all the text below n as it is in the document, empty when
// n is nil.
func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(n)
	return b.String()
}

// collapse trims s and turns every run of whitespace into one space.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
