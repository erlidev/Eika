package filter

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/page"
)

// bindings names everything a filter can use. It ends every failure, so the
// next attempt is written against it rather than guessed at.
const bindings = "Bindings: text, lines[], sections[{heading, level, text, from, to}], grep(re, ctx?), code(lang?)."

// maxOutputBytes bounds what a filter may return. More than this is a bug
// in the filter, not something to cut short quietly.
const maxOutputBytes = 4_000_000

// prelude defines the bindings and the renderer. It is JavaScript because
// grep takes the model's regular expressions, which only a JavaScript engine
// reads the way the model wrote them.
//
//go:embed prelude.js
var prelude string

// section is a section as a filter sees it.
type section struct {
	// Heading is the heading as HeadingText shows it, so a filter can hand
	// it straight back as a section to read.
	Heading string `json:"heading"`
	Level   int    `json:"level"`
	Text    string `json:"text"`
	// Index is the section's place in the page; rendering sorts by it.
	Index int `json:"index"`
	// From and To are the section's inclusive line range in lines.
	From int `json:"from"`
	To   int `json:"to"`
}

// sections splits a page the way fetch does, with the line coordinates grep
// and lines share.
func sections(markdown string) []section {
	split := page.Split(markdown)
	out := make([]section, len(split))
	for i, s := range split {
		// Trailing blank lines are the gap between sections, not part of
		// either.
		text := strings.TrimRight(s.Body, "\n")
		out[i] = section{
			Heading: page.HeadingText(s.Heading),
			Level:   s.Level,
			Text:    text,
			Index:   i,
			From:    s.Start,
			To:      s.Start + strings.Count(text, "\n"),
		}
	}
	return out
}

// rendered is what the prelude's render returns.
type rendered struct {
	Text     string `json:"text"`
	Sections *int   `json:"sections"`
}

// Run runs a filter over a page and returns its answer cut to the budget,
// or the message that replaces it.
func Run(req page.FilterRequest) page.FilterOutcome {
	started := time.Now()
	timeout := page.FilterTimeout
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	tokens := req.Tokens
	if tokens <= 0 {
		tokens = page.ContentTokens
	}
	secs := sections(req.Markdown)
	stats := func(kept string, keptSections *int) page.FilterStats {
		return page.FilterStats{
			Sections:     len(secs),
			KeptSections: keptSections,
			Lines:        strings.Count(req.Markdown, "\n") + 1,
			TotalTokens:  roundDiv(len(req.Markdown), search.CharsPerToken),
			KeptTokens:   roundDiv(len(kept), search.CharsPerToken),
			SandboxMS:    time.Since(started).Milliseconds(),
		}
	}
	fail := func(message string) page.FilterOutcome {
		return page.FilterOutcome{Kind: page.FilterError, Text: message + "\n" + bindings, Stats: stats("", nil)}
	}

	out, err := evaluate(req.Markdown, secs, req.Source, timeout)
	if err != nil {
		return fail(describe(err, timeout))
	}
	if len(out.Text) > maxOutputBytes {
		return fail(fmt.Sprintf("filter returned %s characters. Select less than the whole page.", search.Thousands(len(out.Text))))
	}
	if strings.TrimSpace(out.Text) == "" {
		st := stats("", nil)
		return page.FilterOutcome{Kind: page.FilterEmpty, Text: emptyMessage(secs, st), Stats: st}
	}

	st := stats(out.Text, out.Sections)
	shaped := page.Shape(out.Text, tokens, true)
	outcome := page.FilterOutcome{Kind: page.FilterOK, Text: shaped.Text, Truncated: shaped.Mode != page.Full, Stats: st}
	if !outcome.Truncated {
		outcome.Footer = footer(st)
	}
	return outcome
}

// errTimedOut is the value a run is interrupted with.
var errTimedOut = errors.New("timed out")

// evaluate runs the filter in a fresh interpreter and renders what it
// returned.
func evaluate(markdown string, secs []section, source string, timeout time.Duration) (rendered, error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	timer := time.AfterFunc(timeout, func() { vm.Interrupt(errTimedOut) })
	defer timer.Stop()

	setup, err := vm.RunScript("prelude.js", prelude)
	if err != nil {
		return rendered{}, fmt.Errorf("load prelude: %w", err)
	}
	install, ok := goja.AssertFunction(setup)
	if !ok {
		return rendered{}, errors.New("load prelude: not a function")
	}
	sectionsJSON, err := json.Marshal(secs)
	if err != nil {
		return rendered{}, fmt.Errorf("encode sections: %w", err)
	}
	render, err := install(goja.Undefined(), vm.GlobalObject(), vm.ToValue(markdown), vm.ToValue(string(sectionsJSON)))
	if err != nil {
		return rendered{}, err
	}
	renderFn, ok := goja.AssertFunction(render)
	if !ok {
		return rendered{}, errors.New("load prelude: no renderer")
	}

	program, err := compile(source)
	if err != nil {
		return rendered{}, err
	}
	value, err := vm.RunProgram(program)
	if err != nil {
		return rendered{}, err
	}
	result, err := renderFn(goja.Undefined(), value)
	if err != nil {
		return rendered{}, err
	}
	var out rendered
	if err := vm.ExportTo(result, &out); err != nil {
		return rendered{}, fmt.Errorf("read the rendered answer: %w", err)
	}
	return out, nil
}

// compile wraps the filter so that return is optional. A forgotten return
// is the likeliest mistake, so both forms compile: an expression first, and
// a function body when the source is a statement list.
func compile(source string) (*goja.Program, error) {
	if p, err := goja.Compile("filter.js", "(() => { return (\n"+source+"\n); })()", false); err == nil {
		return p, nil
	}
	return goja.Compile("filter.js", "(() => {\n"+source+"\n})()", false)
}

// describe renders a failure as one line.
func describe(err error, timeout time.Duration) string {
	var interrupted *goja.InterruptedError
	var exception *goja.Exception
	var message string
	switch {
	case errors.As(err, &interrupted):
		message = fmt.Sprintf("filter timed out after %s.", timeout)
	case errors.As(err, &exception):
		message = thrown(exception.Value())
	default:
		message = err.Error()
	}
	return strings.Join(strings.Fields(message), " ")
}

// thrown renders a thrown value as "Name: message", leaving out a plain
// Error's name.
func thrown(v goja.Value) string {
	obj, ok := v.(*goja.Object)
	if !ok {
		return v.String()
	}
	message := obj.Get("message")
	if message == nil || goja.IsUndefined(message) {
		return v.String()
	}
	if name := obj.Get("name"); name != nil && !goja.IsUndefined(name) && name.String() != "Error" {
		return name.String() + ": " + message.String()
	}
	return message.String()
}

// emptyMessage is what a filter that selected nothing returns: a map of the
// page, at the moment the model learned its guess was wrong.
func emptyMessage(secs []section, st page.FilterStats) string {
	var headings []string
	for _, s := range secs {
		if s.Heading != "" {
			headings = append(headings, s.Heading)
		}
	}
	line := fmt.Sprintf("filter matched nothing. Page: %s, %s, ~%s tokens.",
		search.Plural(st.Sections, "section"), search.Plural(st.Lines, "line"), search.Thousands(st.TotalTokens))
	if len(headings) == 0 {
		return line + "\nThis page has no headings."
	}
	return line + "\nHeadings: " + page.HeadingList(headings)
}

// footer states the answer's coordinate space, which is what makes
// lines.slice() usable as pagination.
func footer(st page.FilterStats) string {
	parts := []string{fmt.Sprintf("filtered: ~%s of ~%s tokens", search.Thousands(st.KeptTokens), search.Thousands(st.TotalTokens))}
	if st.KeptSections != nil {
		parts = append(parts, fmt.Sprintf("%d of %s", *st.KeptSections, search.Plural(st.Sections, "section")))
	} else {
		parts = append(parts, search.Plural(st.Sections, "section"))
	}
	parts = append(parts, search.Plural(st.Lines, "line"))
	return "\n\n[" + strings.Join(parts, " · ") + "]"
}

// roundDiv divides and rounds to the nearest integer.
func roundDiv(n, d int) int { return (n + d/2) / d }

// maxRequestBytes bounds a filter request: the fetch body cap, generously
// escaped.
const maxRequestBytes = 16 << 20

// Serve reads one FilterRequest as JSON from r, runs it, and writes the
// FilterOutcome as JSON to w. It is `eikad filter`.
func Serve(r io.Reader, w io.Writer) error {
	var req page.FilterRequest
	if err := json.NewDecoder(io.LimitReader(r, maxRequestBytes)).Decode(&req); err != nil {
		return fmt.Errorf("read filter request: %w", err)
	}
	if err := json.NewEncoder(w).Encode(Run(req)); err != nil {
		return fmt.Errorf("write filter outcome: %w", err)
	}
	return nil
}
