package builtin

import (
	"fmt"

	"github.com/erlidev/eika/internal/tool"
)

// Registry returns a registry holding every built-in tool. It is the one place
// built-in tools are registered; adding a tool means adding it to this list.
// The registry is safe to share between runs because tools are stateless: a
// call gets its workspace from the tool.CallContext.
func Registry() (*tool.Registry, error) {
	r, err := tool.NewRegistry(
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
