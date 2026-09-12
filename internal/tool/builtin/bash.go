package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/tool"
)

// bashTool runs a shell command in the workspace.
type bashTool struct{}

// bashArgs are the parameters of a bash call.
type bashArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// Name identifies the tool to the model.
func (bashTool) Name() string { return "bash" }

// Description tells the model what the tool does.
func (bashTool) Description() string {
	return "Run a shell command in the workspace root. Standard output and standard error are " +
		"combined and returned together with the exit code. Long output is truncated in the middle."
}

// Schema describes the parameters of a bash call.
func (bashTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "The command line to run with sh -c."},
    "timeout": {"type": "integer", "description": "Seconds to allow the command. Defaults to 120, maximum 600."}
  },
  "required": ["command"],
  "additionalProperties": false
}`)
}

// Call runs the command, streaming its output as tool.output events.
func (bashTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[bashArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if strings.TrimSpace(args.Command) == "" {
		return tool.Errorf("bash: command is required"), nil
	}
	timeout := defaultBashTimeout
	if args.Timeout > 0 {
		timeout = min(time.Duration(args.Timeout)*time.Second, maxBashTimeout)
	}

	out := &output{stream: func(chunk string) { c.Output(ctx, chunk) }}
	res, err := c.Exec.Exec(ctx, executor.ExecSpec{
		Command: args.Command,
		Shell:   true,
		Timeout: timeout,
		Stdout:  out,
		Stderr:  out,
	})
	if err != nil {
		return tool.Errorf("bash: %v", err), nil
	}

	var b strings.Builder
	b.WriteString(out.String())
	if res.TimedOut {
		fmt.Fprintf(&b, "\n\ncommand timed out after %s and was killed", timeout)
	} else if res.ExitCode != 0 {
		fmt.Fprintf(&b, "\n\nexit code %d", res.ExitCode)
	}
	details, err := json.Marshal(map[string]any{"exit_code": res.ExitCode, "timed_out": res.TimedOut})
	if err != nil {
		return tool.Result{}, fmt.Errorf("encode bash details: %w", err)
	}
	content := strings.TrimSpace(b.String())
	if content == "" {
		content = "(no output)"
	}
	return tool.Result{
		Content: content,
		IsError: res.TimedOut || res.ExitCode != 0,
		Details: details,
	}, nil
}

var _ tool.Tool = bashTool{}
