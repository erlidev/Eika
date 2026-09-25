package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/search/fetch"
	"github.com/erlidev/eika/internal/search/filter"
	"github.com/erlidev/eika/internal/search/searchtest"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// sandbox answers `eikad filter` the way the daemon in a workspace does, and
// records every command it was asked to run.
type sandbox struct {
	executor.Executor
	commands [][]string
}

func (s *sandbox) Exec(_ context.Context, spec executor.ExecSpec) (executor.ExecResult, error) {
	s.commands = append(s.commands, append([]string{spec.Command}, spec.Args...))
	if spec.Command != eikad.BinaryPath || !slices.Equal(spec.Args, []string{"filter"}) {
		return executor.ExecResult{ExitCode: 127}, nil
	}
	if err := filter.Serve(spec.Stdin, spec.Stdout); err != nil {
		return executor.ExecResult{ExitCode: 1}, nil
	}
	return executor.ExecResult{}, nil
}

func callWeb(t *testing.T, deps builtin.Deps, c tool.CallContext, name string, args any) tool.Result {
	t.Helper()
	r, err := builtin.Registry(deps)
	if err != nil {
		t.Fatal(err)
	}
	tl, _ := r.Get(name)
	raw, _ := json.Marshal(args)
	res, err := tl.Call(t.Context(), c, raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func TestWebSearchRendersResultsAndDetails(t *testing.T) {
	engine, err := search.NewEngine(search.Config{Backends: []search.Backend{{
		Name: "searxng", Web: true,
		Searcher: searchtest.New(search.Result{Title: "Tokio", URL: "https://tokio.rs/", Description: "An async runtime."}),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	res := callWeb(t, builtin.Deps{Search: engine}, tool.CallContext{}, "web_search", map[string]any{"query": "tokio", "count": 3})
	if res.IsError || res.Content != "1. Tokio\n   https://tokio.rs/\n   An async runtime." {
		t.Errorf("content = %q", res.Content)
	}
	var details search.Details
	if err := json.Unmarshal(res.Details, &details); err != nil || details.Count != 3 || len(details.Results) != 1 || details.Providers[0] != "searxng" {
		t.Errorf("details = %s, %v", res.Details, err)
	}

	bad := callWeb(t, builtin.Deps{Search: engine}, tool.CallContext{}, "web_search", map[string]any{"query": ""})
	if !bad.IsError || bad.Content != "web search failed: query is empty." {
		t.Errorf("empty query = %q", bad.Content)
	}
}

func TestWebToolsWithoutTheirDependenciesFail(t *testing.T) {
	if res := callWeb(t, builtin.Deps{}, tool.CallContext{}, "web_search", map[string]any{"query": "q"}); !res.IsError {
		t.Error("web_search without a searcher succeeded")
	}
	if res := callWeb(t, builtin.Deps{}, tool.CallContext{}, "web_fetch", map[string]any{"url": "https://x"}); !res.IsError {
		t.Error("web_fetch without a reader succeeded")
	}
}

func newPages() *fetch.Reader {
	client, _ := searchtest.Client(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Guide\n\nintro\n\n## Timeouts\n\nthe timeout is 30s\n"))
	})
	return fetch.New(fetch.Config{Client: client})
}

func TestWebFetchReadsAPage(t *testing.T) {
	res := callWeb(t, builtin.Deps{Pages: newPages()}, tool.CallContext{}, "web_fetch",
		map[string]any{"url": "https://example.com/guide.md", "section": "timeouts"})
	if res.IsError || res.Content != "## Timeouts\n\nthe timeout is 30s" {
		t.Errorf("content = %q", res.Content)
	}
	var details fetch.Details
	if err := json.Unmarshal(res.Details, &details); err != nil || details.Section != "timeouts" || details.Mode != "full" {
		t.Errorf("details = %s, %v", res.Details, err)
	}
}

func TestWebFetchRunsFiltersInTheSandbox(t *testing.T) {
	box := &sandbox{}
	res := callWeb(t, builtin.Deps{Pages: newPages()}, tool.CallContext{Exec: box}, "web_fetch",
		map[string]any{"url": "https://example.com/guide.md", "filter": "grep(/30s/, 0)"})
	if res.IsError || !strings.HasPrefix(res.Content, "Timeouts · lines[6..6]\nthe timeout is 30s") {
		t.Errorf("content = %q", res.Content)
	}
	if len(box.commands) != 1 || !slices.Equal(box.commands[0], []string{eikad.BinaryPath, "filter"}) {
		t.Errorf("commands = %v, want one eikad filter", box.commands)
	}
}

// failing is a sandbox whose commands cannot run.
type failing struct{ executor.Executor }

func (failing) Exec(context.Context, executor.ExecSpec) (executor.ExecResult, error) {
	return executor.ExecResult{}, errors.New("workspace is stopped")
}

func TestWebFetchReportsASandboxThatCannotRunTheFilter(t *testing.T) {
	res := callWeb(t, builtin.Deps{Pages: newPages()}, tool.CallContext{Exec: failing{}}, "web_fetch",
		map[string]any{"url": "https://example.com/guide.md", "filter": "text"})
	if !res.IsError || !strings.HasPrefix(res.Content, "fetch failed: the filter could not run in the sandbox") {
		t.Errorf("content = %q", res.Content)
	}
}

// A chat has no workspace, so the model's JavaScript has nowhere to run; the
// page itself is still a network read the harness makes.
func TestWebFetchInAChatRefusesAFilterButReadsThePage(t *testing.T) {
	chat := tool.CallContext{}
	res := callWeb(t, builtin.Deps{Pages: newPages()}, chat, "web_fetch",
		map[string]any{"url": "https://example.com/guide.md", "filter": "text"})
	if !res.IsError || !strings.Contains(res.Content, "this chat has none") {
		t.Errorf("filter in a chat = %q", res.Content)
	}
	res = callWeb(t, builtin.Deps{Pages: newPages()}, chat, "web_fetch",
		map[string]any{"url": "https://example.com/guide.md", "section": "timeouts"})
	if res.IsError || res.Content != "## Timeouts\n\nthe timeout is 30s" {
		t.Errorf("section in a chat = %q", res.Content)
	}
}
