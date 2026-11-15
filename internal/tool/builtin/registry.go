package builtin

import (
	"fmt"

	"github.com/erlidev/eika/internal/tool"
)

// Deps are what the tools that reach beyond the computer are given.
type Deps struct {
	// Questions is the broker ask_user waits on.
	Questions *Questions
	// Search runs web_search; Pages reads for web_fetch.
	Search Searcher
	Pages  PageReader
}

// Registry returns a registry holding every built-in tool. It is the one place
// built-in tools are registered; adding a tool means adding it to this list.
// The registry is safe to share between runs because tools are stateless: a
// call gets its computer from the tool.CallContext.
//
// A nil dependency leaves its tools registered but failing, which is what a
// run nobody can answer wants.
func Registry(d Deps) (*tool.Registry, error) {
	r, err := tool.NewRegistry(
		askUserTool{questions: d.Questions},
		webSearchTool{search: d.Search},
		webFetchTool{pages: d.Pages},
		bashTool{},
		fileTool{name: "file_read"},
		fileTool{name: "file_write"},
		fileTool{name: "file_edit"},
	)
	if err != nil {
		return nil, fmt.Errorf("build built-in tool registry: %w", err)
	}
	return r, nil
}
