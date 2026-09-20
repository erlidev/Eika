package fetch

import (
	"cmp"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/erlidev/eika/internal/search/page"
)

// blockAtoms are the elements that start a block of their own in markdown.
var blockAtoms = map[atom.Atom]bool{
	atom.Address: true, atom.Article: true, atom.Aside: true, atom.Blockquote: true,
	atom.Body: true, atom.Caption: true, atom.Center: true, atom.Dd: true, atom.Details: true,
	atom.Dialog: true, atom.Dir: true, atom.Div: true, atom.Dl: true, atom.Dt: true,
	atom.Fieldset: true, atom.Figcaption: true, atom.Figure: true, atom.Footer: true,
	atom.Form: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true,
	atom.H6: true, atom.Header: true, atom.Hgroup: true, atom.Hr: true, atom.Html: true,
	atom.Legend: true, atom.Li: true, atom.Main: true, atom.Menu: true, atom.Nav: true,
	atom.Ol: true, atom.P: true, atom.Pre: true, atom.Section: true, atom.Summary: true,
	atom.Table: true, atom.Tbody: true, atom.Td: true, atom.Tfoot: true, atom.Th: true,
	atom.Thead: true, atom.Tr: true, atom.Ul: true,
}

// inlineOnly are the elements that stay inline whatever they hold. A link
// around a block of text is still one link.
var inlineOnly = map[atom.Atom]bool{
	atom.A: true, atom.B: true, atom.Strong: true, atom.I: true, atom.Em: true,
	atom.Code: true, atom.Kbd: true, atom.Samp: true, atom.Tt: true, atom.Del: true,
	atom.S: true, atom.Strike: true, atom.Q: true,
}

// urlEscaper keeps a URL from ending a markdown link early.
var urlEscaper = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29")

// converter renders a document tree as markdown.
type converter struct {
	base *url.URL
	// blocky remembers which elements outside blockAtoms hold a block, so
	// that the question is answered once per element.
	blocky map[*html.Node]bool
}

// newConverter returns a converter that resolves links against base.
func newConverter(base *url.URL) *converter {
	return &converter{base: base, blocky: map[*html.Node]bool{}}
}

// render returns the markdown of n and everything below it.
func (c *converter) render(n *html.Node) string {
	return strings.Join(c.blocks(n), "\n\n")
}

// blocks renders the children of a block container: each block child as
// blocks of its own, and each run of inline children as one paragraph.
func (c *converter) blocks(n *html.Node) []string {
	var out []string
	var run strings.Builder
	flush := func() {
		if p := paragraph(run.String()); p != "" {
			out = append(out, p)
		}
		run.Reset()
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if c.isBlock(ch) {
			flush()
			out = append(out, c.block(ch)...)
			continue
		}
		run.WriteString(c.inline(ch))
	}
	flush()
	return out
}

// block renders one block element.
func (c *converter) block(n *html.Node) []string {
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		text := collapse(c.inlineChildren(n))
		if text == "" {
			return nil
		}
		level := int(n.Data[1] - '0')
		return []string{strings.Repeat("#", level) + " " + text}
	case atom.Dt, atom.Summary, atom.Legend, atom.Caption:
		if text := collapse(c.inlineChildren(n)); text != "" {
			return []string{"**" + text + "**"}
		}
		return nil
	case atom.Pre:
		if code := codeBlock(n); code != "" {
			return []string{code}
		}
		return nil
	case atom.Blockquote:
		inner := c.blocks(n)
		if len(inner) == 0 {
			return nil
		}
		return []string{quote(strings.Join(inner, "\n\n"))}
	case atom.Ul, atom.Ol, atom.Menu, atom.Dir:
		if list := c.list(n); list != "" {
			return []string{list}
		}
		return nil
	case atom.Table:
		return c.table(n)
	case atom.Hr:
		return []string{"---"}
	}
	return c.blocks(n)
}

// isBlock reports whether a node renders as a block: a block element, or an
// element that is not inline by nature and holds a block.
func (c *converter) isBlock(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if blockAtoms[n.DataAtom] {
		return true
	}
	if inlineOnly[n.DataAtom] {
		return false
	}
	if v, ok := c.blocky[n]; ok {
		return v
	}
	v := false
	for ch := n.FirstChild; ch != nil && !v; ch = ch.NextSibling {
		v = c.isBlock(ch)
	}
	c.blocky[n] = v
	return v
}

// inline renders an inline node.
func (c *converter) inline(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return collapseSpace(n.Data)
	case html.ElementNode:
	default:
		return ""
	}
	switch n.DataAtom {
	case atom.Br:
		return "\n"
	case atom.Strong, atom.B:
		return wrap(c.inlineChildren(n), "**", "**")
	case atom.Em, atom.I:
		return wrap(c.inlineChildren(n), "_", "_")
	case atom.Del, atom.S, atom.Strike:
		return wrap(c.inlineChildren(n), "~~", "~~")
	case atom.Q:
		return wrap(c.inlineChildren(n), `"`, `"`)
	case atom.Code, atom.Kbd, atom.Samp, atom.Tt:
		return codeSpan(textOf(n))
	case atom.A:
		return c.link(n)
	case atom.Img:
		return c.image(n)
	}
	return c.inlineChildren(n)
}

// inlineChildren renders the children of an inline element. A block that
// turns up inside one is flattened to its text, and a code block to a code
// span, because markdown has no way to put a block inside a link, an
// emphasis, or a table cell.
func (c *converter) inlineChildren(n *html.Node) string {
	var b strings.Builder
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.DataAtom == atom.Pre {
			b.WriteString(" " + codeSpan(textOf(ch)) + " ")
			continue
		}
		if c.isBlock(ch) {
			b.WriteString(" " + c.inlineChildren(ch) + " ")
			continue
		}
		b.WriteString(c.inline(ch))
	}
	return b.String()
}

// link renders an anchor. A link within the page keeps its short fragment:
// expanding fifty of them to absolute URLs costs tokens and says nothing new.
// A link that is not to a web or mail address keeps its text and loses its
// target.
func (c *converter) link(n *html.Node) string {
	inner := strings.ReplaceAll(c.inlineChildren(n), "\n", " ")
	text := strings.TrimSpace(inner)
	if text == "" {
		return inner
	}
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" {
		return inner
	}
	if strings.HasPrefix(href, "#") {
		return wrap(inner, "[", "]("+urlEscaper.Replace(href)+")")
	}
	target := resolve(c.base, href)
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
		return inner
	}
	return wrap(inner, "[", "]("+urlEscaper.Replace(target)+")")
}

// image renders an image by its alternative text. An image without one is
// decoration and is left out, and an inlined data image is its text alone: a
// single one can outweigh the whole budget.
func (c *converter) image(n *html.Node) string {
	alt := collapse(attr(n, "alt"))
	if alt == "" {
		return ""
	}
	if strings.HasPrefix(strings.TrimSpace(attr(n, "src")), "data:") {
		return alt
	}
	src := resolve(c.base, attr(n, "src"))
	if u, err := url.Parse(src); src == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return alt
	}
	return "![" + alt + "](" + urlEscaper.Replace(src) + ")"
}

// list renders a list, indenting what an item holds past its marker so that
// nested lists and paragraphs stay inside the item.
func (c *converter) list(n *html.Node) string {
	ordered := n.DataAtom == atom.Ol
	number := 1
	if start, err := strconv.Atoi(attr(n, "start")); ordered && err == nil {
		number = start
	}
	var items []string
	for li := n.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.DataAtom != atom.Li {
			continue
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(number) + ". "
			number++
		}
		parts := c.blocks(li)
		if len(parts) == 0 {
			continue
		}
		pad := strings.Repeat(" ", len(marker))
		item := marker + indent(parts[0], pad, false)
		for _, p := range parts[1:] {
			item += "\n" + indent(p, pad, true)
		}
		items = append(items, item)
	}
	return strings.Join(items, "\n")
}

// table renders a table as a GitHub table, its first row the header. A
// cell's blocks are flattened to one line, since a row cannot span lines.
func (c *converter) table(n *html.Node) []string {
	var out []string
	var rows [][]*html.Node
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type != html.ElementNode {
				continue
			}
			switch ch.DataAtom {
			case atom.Caption:
				out = append(out, c.block(ch)...)
			case atom.Tr:
				var cells []*html.Node
				for cell := ch.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.DataAtom == atom.Td || cell.DataAtom == atom.Th {
						cells = append(cells, cell)
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			case atom.Thead, atom.Tbody, atom.Tfoot:
				collect(ch)
			}
		}
	}
	collect(n)
	if len(rows) == 0 {
		return out
	}

	cols := 0
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	lines := make([]string, 0, len(rows)+1)
	for i, row := range rows {
		cells := make([]string, cols)
		for j, cell := range row {
			cells[j] = strings.ReplaceAll(collapse(c.inlineChildren(cell)), "|", `\|`)
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
		if i == 0 {
			lines = append(lines, "|"+strings.Repeat(" --- |", cols))
		}
	}
	return append(out, strings.Join(lines, "\n"))
}

// codeBlock renders <pre> as a fenced block, naming the language when a
// class says it.
func codeBlock(pre *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			b.WriteString(n.Data)
		case n.DataAtom == atom.Br:
			b.WriteString("\n")
		default:
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				visit(ch)
			}
		}
	}
	visit(pre)
	code := strings.Trim(b.String(), "\n")
	if strings.TrimSpace(code) == "" {
		return ""
	}
	return page.Fence(code, language(pre))
}

// languages are the names a highlighter may use as a code block's only
// class, as rustdoc and some hand-rolled themes do.
var languages = []string{
	"bash", "c", "cpp", "csharp", "css", "diff", "go", "graphql", "haskell", "hcl", "html", "ini",
	"java", "javascript", "json", "jsx", "kotlin", "lua", "makefile", "markdown", "nix", "objc",
	"ocaml", "perl", "php", "protobuf", "python", "r", "ruby", "rust", "scala", "sh", "shell", "sql",
	"svelte", "swift", "toml", "ts", "tsx", "typescript", "vue", "xml", "yaml", "zig",
}

// languageClass is a class naming a code block's language.
var languageClass = regexp.MustCompile(`(?i)(?:^|\s)(?:language|lang|highlight|highlight-source)-([\w+#-]+)`)

// language reads a code block's language. Generators put it on the code
// element, the <pre>, or a wrapper up to a few levels out, as a data
// attribute, a prefixed class, or a bare language name.
func language(pre *html.Node) string {
	n := findFirst(pre, isAtom(atom.Code))
	if n == nil {
		n = pre
	}
	for depth := 0; n != nil && n.Type == html.ElementNode && depth < 4; depth, n = depth+1, n.Parent {
		if data := cmp.Or(attr(n, "data-lang"), attr(n, "data-language")); data != "" {
			return normalizeLanguage(data)
		}
		class := attr(n, "class")
		if m := languageClass.FindStringSubmatch(class); m != nil {
			return normalizeLanguage(m[1])
		}
		for _, token := range strings.Fields(class) {
			if lang := strings.ToLower(token); slices.Contains(languages, lang) {
				return lang
			}
		}
	}
	return ""
}

// normalizeLanguage lowercases a language and drops the names highlighters
// give unhighlighted blocks.
func normalizeLanguage(raw string) string {
	switch lang := strings.ToLower(strings.TrimSpace(raw)); lang {
	case "none", "text", "plaintext":
		return ""
	default:
		return lang
	}
}

// codeSpan renders inline code, with a fence longer than any run of
// backticks inside it.
func codeSpan(text string) string {
	text = collapse(text)
	if text == "" {
		return ""
	}
	fence := "`"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	if len(fence) > 1 {
		return fence + " " + text + " " + fence
	}
	return fence + text + fence
}

// wrap puts open and close around the text of s, keeping its surrounding
// spaces outside them, so that "a <b> b </b>c" becomes "a **b** c".
func wrap(s, open, close string) string {
	text := strings.TrimSpace(s)
	if text == "" {
		return s
	}
	lead := s[:strings.Index(s, text)]
	trail := s[len(lead)+len(text):]
	return lead + open + text + close + trail
}

// paragraph tidies a run of inline markdown: single spaces, no blank lines
// at its edges, and the line breaks <br> asked for.
func paragraph(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, line := range lines {
		out = append(out, collapse(line))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// quote prefixes every line of s for a blockquote.
func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

// indent pads the lines of s with pad: every line when all is set, every
// line but the first otherwise. Blank lines stay blank.
func indent(s, pad string, all bool) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" || (i == 0 && !all) {
			continue
		}
		lines[i] = pad + line
	}
	return strings.Join(lines, "\n")
}

// collapseSpace turns every run of whitespace, no-break spaces included,
// into one space, keeping a single space at either end if there was one.
func collapseSpace(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', ' ':
			if !space {
				b.WriteByte(' ')
			}
			space = true
		default:
			b.WriteRune(r)
			space = false
		}
	}
	return b.String()
}

// resolve makes a link in a page absolute. A link that does not parse comes
// back empty and is dropped by the caller.
func resolve(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	return u.String()
}
