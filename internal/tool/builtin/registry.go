package builtin

import (
	"context"
	"fmt"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/sentry"

	"github.com/erlidev/eika/internal/tool"
)

// Deps are what the tools that reach beyond the computer are given.
type Deps struct {
	// Questions is the broker ask_user waits on.
	Questions *Questions
	// QuestionRelay handles subagent questions; false keeps the legacy broker.
	QuestionRelay func(context.Context, Question) (string, bool, error)
	Projects      Projects
	History       History
	// ScreenHistory screens text a history tool reads from a child session.
	ScreenHistory Screen
	HomeExecutor  func(context.Context, string) (executor.Executor, func(), error)
	// Subagents runs the delegation tools for the calling home session.
	Subagents Subagents
	// Reports takes a subagent's report.
	Reports Reports
	// Search runs web_search; Pages reads for web_fetch.
	Search Searcher
	Pages  PageReader
	// Memory is the memory writer; MemoryCheck is Sentry's write check, which
	// every memory_write passes before the writer.
	Memory      Memories
	MemoryCheck sentry.MemoryChecker
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
		historyTool{name: "history_search", history: d.History, screen: d.ScreenHistory},
		historyTool{name: "history_read", history: d.History, screen: d.ScreenHistory},
		askUserTool{questions: d.Questions, relay: d.QuestionRelay},
		projectTool{name: "project_list", projects: d.Projects, home: d.HomeExecutor},
		projectTool{name: "project_open", projects: d.Projects, home: d.HomeExecutor},
		projectTool{name: "project_create", projects: d.Projects, home: d.HomeExecutor},
		projectTool{name: "project_update", projects: d.Projects, home: d.HomeExecutor},
		subagentTool{name: "subagent_start", subagents: d.Subagents, projects: d.Projects},
		subagentTool{name: "subagent_message", subagents: d.Subagents, projects: d.Projects},
		subagentTool{name: "subagent_status", subagents: d.Subagents, projects: d.Projects},
		subagentTool{name: "subagent_cancel", subagents: d.Subagents, projects: d.Projects},
		reportTool{reports: d.Reports},
		webSearchTool{search: d.Search},
		webFetchTool{pages: d.Pages},
		memoryTool{name: "memory_search", memory: d.Memory, check: d.MemoryCheck},
		memoryTool{name: "memory_read", memory: d.Memory, check: d.MemoryCheck},
		memoryTool{name: "memory_write", memory: d.Memory, check: d.MemoryCheck},
		memoryTool{name: "memory_forget", memory: d.Memory, check: d.MemoryCheck},
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
