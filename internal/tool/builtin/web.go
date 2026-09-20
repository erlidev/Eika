package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/search/page"
	"github.com/erlidev/eika/internal/tool"
)

// filterExecTimeout bounds the sandbox process a filter runs in: the
// filter's own timeout, plus starting the process and reading the page.
const filterExecTimeout = page.FilterTimeout + 10*time.Second

// Searcher runs a search. search.Engine is the one implementation and the
// server injects it.
type Searcher interface {
	Search(ctx context.Context, req search.Request) (search.Outcome, error)
}

// PageReader reads a web page. fetch.Reader is the one implementation and
// the server injects it.
type PageReader interface {
	Read(ctx context.Context, req fetch.Request, filter fetch.FilterFunc) (fetch.Outcome, error)
}

// webSearchTool searches the web and a few sources that rank their own
// content well.
type webSearchTool struct {
	search Searcher
}

// webSearchArgs are the parameters of a web_search call.
type webSearchArgs struct {
	Query  string `json:"query"`
	Source string `json:"source"`
	Count  int    `json:"count"`
}

// Name is the identifier the model calls the tool by.
func (webSearchTool) Name() string { return "web_search" }

// Description tells the model what the tool does. Every word of it is paid
// for on every request, so it says only what the model needs before its
// first call; the rest arrives in the results when it is useful.
func (webSearchTool) Description() string {
	return "Search the web, Wikipedia, arXiv or GitHub. Returns titles, URLs and snippets. " +
		"Use it when you need current information, documentation, or prior art you do not already have."
}

// Schema describes the parameters of a web_search call.
func (webSearchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "Search query. arxiv accepts natural text or ti:, au:, abs:, cat: fields joined by AND/OR/ANDNOT. GitHub sources accept qualifiers such as language:rust, repo:owner/name, is:open, is:pr, is:issue."},
    "source": {
      "type": "string",
      "enum": ["web", "wikipedia", "arxiv", "github_code", "github_repos", "github_issues"],
      "description": "Where to search. Defaults to web."
    },
    "count": {"type": "integer", "minimum": 1, "maximum": 25, "description": "Results to return, 1-25. Defaults to 10."}
  },
  "required": ["query"],
  "additionalProperties": false
}`)
}

// Call runs the search.
func (t webSearchTool) Call(ctx context.Context, _ tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[webSearchArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if t.search == nil {
		return tool.Errorf("web_search: this harness has no search configured"), nil
	}
	out, err := t.search.Search(ctx, search.Request{Query: args.Query, Source: args.Source, Count: args.Count})
	if err != nil {
		if ctx.Err() != nil {
			return tool.Result{}, fmt.Errorf("web_search: %w", ctx.Err())
		}
		return tool.Errorf("web_search: %v", err), nil
	}
	details, err := json.Marshal(out.Details)
	if err != nil {
		return tool.Result{}, fmt.Errorf("encode web_search details: %w", err)
	}
	return tool.Result{Content: out.Text, IsError: out.IsError, Details: details}, nil
}

// webFetchTool reads a page as Markdown.
type webFetchTool struct {
	pages PageReader
}

// webFetchArgs are the parameters of a web_fetch call.
type webFetchArgs struct {
	URL     string `json:"url"`
	Section string `json:"section"`
	Filter  string `json:"filter"`
	Format  string `json:"format"`
}

// Name is the identifier the model calls the tool by.
func (webFetchTool) Name() string { return "web_fetch" }

// Description tells the model what the tool does, and the ladder for reading
// a page too long to take whole.
func (webFetchTool) Description() string {
	return strings.Join([]string{
		"Fetch a web page or file by URL and return its content as Markdown.",
		"Use it to read a page you already have a URL for. Use web_search to find URLs.",
		"GitHub URLs resolve through the API: repository, blob, tree, issue, pull request, release and gist URLs all work directly, " +
			"and return the underlying Markdown or source rather than the rendered page.",
		"No heading in hand: web_fetch(url). Over budget it returns the page outline.",
		`Heading in hand: section: "<heading>".`,
		"Term, pattern, question or line range: filter:. The page is cached, so retrying a filter is free.",
		"A URL fragment naming a heading returns that section; one naming anything else returns the page.",
	}, "\n")
}

// Schema describes the parameters of a web_fetch call.
func (webFetchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {"type": "string", "description": "Absolute http(s) URL."},
    "section": {"type": "string", "description": "Return only this section of the page, with its subsections. Match is on heading text, case-insensitive, so a heading printed in an outline or a truncation notice can be passed as-is. A URL fragment does the same thing."},
    "filter": {"type": "string", "description": "JS expression run over the page before it enters context. Bindings: text, lines, sections ({heading, level, text, from, to}), grep(re, ctx?) → matching lines ±ctx, adjacent runs merged, code(lang?). Return a string, a section, or an array of either. Examples: grep(/timeout/i, 3) · sections.filter(s => /error/i.test(s.heading)) · code(\"python\") · lines.slice(500, 900)"},
    "format": {
      "type": "string",
      "enum": ["markdown", "text", "raw"],
      "description": "markdown (default), text (markup stripped), or raw (the unprocessed response body)."
    }
  },
  "required": ["url"],
  "additionalProperties": false
}`)
}

// Call reads the page. The model's filter runs inside the workspace sandbox,
// never in the harness.
func (t webFetchTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[webFetchArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if t.pages == nil {
		return tool.Errorf("web_fetch: this harness has no fetch configured"), nil
	}
	req := fetch.Request{URL: args.URL, Section: args.Section, Filter: args.Filter, Format: args.Format}
	out, err := t.pages.Read(ctx, req, sandboxFilter(c.Exec))
	if err != nil {
		if ctx.Err() != nil {
			return tool.Result{}, fmt.Errorf("web_fetch: %w", ctx.Err())
		}
		return tool.Errorf("web_fetch: %v", err), nil
	}
	details, err := json.Marshal(out.Details)
	if err != nil {
		return tool.Result{}, fmt.Errorf("encode web_fetch details: %w", err)
	}
	return tool.Result{Content: out.Text, IsError: out.IsError, Details: details}, nil
}

// sandboxFilter runs a filter as `eikad filter` in the workspace, which is
// where the model's JavaScript belongs.
func sandboxFilter(exec executor.Executor) fetch.FilterFunc {
	return func(ctx context.Context, req page.FilterRequest) (page.FilterOutcome, error) {
		if exec == nil {
			return page.FilterOutcome{}, fmt.Errorf("no workspace to run it in")
		}
		in, err := json.Marshal(req)
		if err != nil {
			return page.FilterOutcome{}, fmt.Errorf("encode filter request: %w", err)
		}
		var stdout, stderr bytes.Buffer
		res, err := exec.Exec(ctx, executor.ExecSpec{
			Command: eikad.BinaryPath,
			Args:    []string{"filter"},
			Stdin:   bytes.NewReader(in),
			Stdout:  &stdout,
			Stderr:  &stderr,
			Timeout: filterExecTimeout,
		})
		switch {
		case err != nil:
			return page.FilterOutcome{}, err
		case res.TimedOut:
			return page.FilterOutcome{}, fmt.Errorf("the filter process timed out")
		case res.ExitCode != 0:
			return page.FilterOutcome{}, fmt.Errorf("eikad filter exited with %d: %s", res.ExitCode, strings.TrimSpace(stderr.String()))
		}
		var out page.FilterOutcome
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
			return page.FilterOutcome{}, fmt.Errorf("read filter outcome: %w", err)
		}
		return out, nil
	}
}

var (
	_ tool.Tool = webSearchTool{}
	_ tool.Tool = webFetchTool{}
)
