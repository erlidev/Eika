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

// Description tells the model what the tool does. There are no separate file
// tools: bash is how the model reads, writes, and edits files, so the
// description says how to do that well and leaves the rest to the model.
func (bashTool) Description() string {
	return `Run a shell command with sh -c in the workspace root. Standard output and standard error are combined and returned with the exit code; long output is truncated in the middle.

This is also how you explore, read, and edit files:
- Search with rg (or grep -rn), find files with rg --files or find, and list directories with ls.
- Read with cat, sed -n, head, or tail; take the range you need rather than a whole large file.
- Create or rewrite a file with a quoted heredoc (cat > path <<'EOF').
- Make small edits with sed -i, and larger or multi-line ones with a short script. Read the text you are replacing first, and check the file after a change that matters.
- Chain related steps with && and pipes in one call, instead of one call per step.`
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
