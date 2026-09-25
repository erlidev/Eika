package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
)

// Tool is one action the model can ask for. Implementations are stateless: a
// call gets everything it needs from its CallContext and arguments.
type Tool interface {
	// Name is the identifier the model calls the tool by.
	Name() string
	// Description tells the model what the tool does and when to use it.
	Description() string
	// Schema is the JSON Schema of the tool's parameters.
	Schema() json.RawMessage
	// Call runs the tool. It returns an error only when the failure is the
	// harness's, not the model's: a bad argument or a missing file is a
	// Result with IsError set, which the model sees and can recover from.
	Call(ctx context.Context, c CallContext, args json.RawMessage) (Result, error)
}

// Standalone is implemented by a tool that runs without a workspace, and so
// may be offered in a chat. Its CallContext.Exec is nil there: the tool works
// without it or answers the model with what it cannot do.
//
// A tool that does not implement it needs a workspace, which keeps a new tool
// out of chats until its author says it belongs there.
type Standalone interface {
	Tool
	// Standalone marks the tool. It is never called.
	Standalone()
}

// NeedsWorkspace reports whether t can run only in a session with a
// workspace.
func NeedsWorkspace(t Tool) bool {
	_, standalone := t.(Standalone)
	return !standalone
}

// CallContext is everything one tool call may use. Exec is the only way to
// reach files and processes, and is nil in a session with no workspace; Emit
// streams partial output to watching clients.
type CallContext struct {
	Exec        executor.Executor
	Emit        event.Emitter
	WorkspaceID string
	SessionID   string
	RunID       string
	CallID      string
}

// Output streams a piece of a running tool's output as a tool.output event.
// A call context with no emitter drops it.
func (c CallContext) Output(ctx context.Context, text string) {
	if c.Emit == nil || text == "" {
		return
	}
	e, err := event.New(event.TypeToolOutput, event.SessionTopic(c.SessionID), event.ToolOutput{
		RunID:  c.RunID,
		CallID: c.CallID,
		Text:   text,
	})
	if err != nil {
		return
	}
	c.Emit.Emit(ctx, e)
}

// Result is what a tool call produced. Content is what the model sees; Details
// is optional structured data for the user interface only.
type Result struct {
	Content string
	IsError bool
	Details json.RawMessage
}

// Text returns a successful result carrying content.
func Text(content string) Result { return Result{Content: content} }

// Errorf returns a failed result whose content is the message the model sees.
func Errorf(format string, args ...any) Result {
	return Result{Content: fmt.Sprintf(format, args...), IsError: true}
}
