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

// Standalone is implemented by a tool that runs without a computer, and so
// may be offered in a chat. Its CallContext.Exec is nil there: the tool works
// without it or answers the model with what it cannot do.
//
// A tool that does not implement it needs a computer, which keeps a new tool
// out of chats until its author says it belongs there.
type Standalone interface {
	Tool
	// Standalone marks the tool. It is never called.
	Standalone()
}

// AgentOnly marks a tool that is available only through an agent definition.
// Legacy profiles and sessions cannot select it.
type AgentOnly interface {
	Tool
	AgentOnly()
}

// ReadOnlyTool is implemented by a tool that changes nothing and reaches
// nowhere outside Eika, for the calls it answers true for. The gate's
// failure mode keys on it: when the gate cannot answer, only such calls run.
// A tool that does not implement it is treated as side-effecting.
type ReadOnlyTool interface {
	Tool
	// ReadOnly reports whether this tool, as named, is read-only.
	ReadOnly() bool
}

// SideEffecting reports whether t may change state or reach beyond Eika.
// Anything not declared read-only is.
func SideEffecting(t Tool) bool {
	r, ok := t.(ReadOnlyTool)
	return !ok || !r.ReadOnly()
}

// NeedsComputer reports whether t can run only in a session with a
// computer.
func NeedsComputer(t Tool) bool {
	_, standalone := t.(Standalone)
	return !standalone
}

// CallContext is everything one tool call may use. Exec is the only way to
// reach files and processes, and is nil in a session with no computer; Emit
// streams partial output to watching clients.
type CallContext struct {
	Exec       executor.Executor
	Emit       event.Emitter
	ComputerID string
	SessionID  string
	RunID      string
	CallID     string
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
//
// Parts, when set, are Content cut into the pieces that came from different
// places, joined by a blank line. The agent ingests each untrusted part on its
// own and rebuilds Content from them.
type Result struct {
	Content string
	// SourceURL identifies the actual web content source; empty means unknown.
	SourceURL string
	IsError   bool
	Details   json.RawMessage
	Parts     []Part
	// Removed counts the windows ingest removed from the result. The agent
	// sets it; no tool does.
	Removed int
}

// Part is one piece of a result. Source names where an untrusted part came
// from (a URL); a part with no Source is the harness's own text.
type Part struct {
	Source string
	Text   string
}

// Text returns a successful result carrying content.
func Text(content string) Result { return Result{Content: content} }

// Errorf returns a failed result whose content is the message the model sees.
func Errorf(format string, args ...any) Result {
	return Result{Content: fmt.Sprintf(format, args...), IsError: true}
}
