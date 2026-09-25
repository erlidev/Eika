package builtin

import (
	"fmt"

	"github.com/erlidev/eika/internal/tool"
)

// Deps are what the tools that reach beyond the workspace are given.
type Deps struct {
	// Questions is the broker ask_user waits on.
	Questions *Questions
	// Agents is the spawner the agent tools reach children through.
	Agents Subagents
	// Search runs web_search; Pages reads for web_fetch.
	Search Searcher
	Pages  PageReader
}

// Registry returns a registry holding every built-in tool. It is the one place
// built-in tools are registered; adding a tool means adding it to this list.
// The registry is safe to share between runs because tools are stateless: a
// call gets its workspace from the tool.CallContext.
//
// A nil dependency leaves its tools registered but failing, which is what a
// run nobody can answer and a harness that spawns nothing want.
func Registry(d Deps) (*tool.Registry, error) {
	r, err := tool.NewRegistry(
		askUserTool{questions: d.Questions},
		spawnAgentTool{agents: d.Agents},
		waitAgentsTool{agents: d.Agents},
		listAgentsTool{agents: d.Agents},
		webSearchTool{search: d.Search},
		webFetchTool{pages: d.Pages},
		bashTool{},
	)
	if err != nil {
		return nil, fmt.Errorf("build built-in tool registry: %w", err)
	}
	return r, nil
}
