package builtin

import (
	"fmt"

	"github.com/erlidev/eika/internal/tool"
)

// Registry returns a registry holding every built-in tool. It is the one place
// built-in tools are registered; adding a tool means adding it to this list.
// The registry is safe to share between runs because tools are stateless: a
// call gets its workspace from the tool.CallContext.
//
// questions is the broker ask_user waits on and agents is the spawner the
// agent tools reach children through. A nil dependency leaves its tools
// registered but failing, which is what a run nobody can answer and a harness
// that spawns nothing want.
func Registry(questions *Questions, agents Subagents) (*tool.Registry, error) {
	r, err := tool.NewRegistry(
		askUserTool{questions: questions},
		spawnAgentTool{agents: agents},
		waitAgentsTool{agents: agents},
		listAgentsTool{agents: agents},
		readTool{},
		writeTool{},
		editTool{},
		bashTool{},
		grepTool{},
		findTool{},
		lsTool{},
	)
	if err != nil {
		return nil, fmt.Errorf("build built-in tool registry: %w", err)
	}
	return r, nil
}
