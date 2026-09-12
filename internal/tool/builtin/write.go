package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/erlidev/eika/internal/tool"
)

// writeTool creates or replaces a file.
type writeTool struct{}

// writeArgs are the parameters of a write call.
type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Name identifies the tool to the model.
func (writeTool) Name() string { return "write" }

// Description tells the model what the tool does.
func (writeTool) Description() string {
	return "Write a file in the workspace, creating parent directories as needed. " +
		"The file is replaced whole; to change part of an existing file use edit."
}

// Schema describes the parameters of a write call.
func (writeTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path relative to the workspace root."},
    "content": {"type": "string", "description": "The complete new contents of the file."}
  },
  "required": ["path", "content"],
  "additionalProperties": false
}`)
}

// Call writes the file.
func (writeTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[writeArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if args.Path == "" {
		return tool.Errorf("write: path is required"), nil
	}
	if err := c.Exec.WriteFile(ctx, args.Path, []byte(args.Content)); err != nil {
		return tool.Errorf("write %s: %v", args.Path, err), nil
	}
	return tool.Text(fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path)), nil
}

var _ tool.Tool = writeTool{}
