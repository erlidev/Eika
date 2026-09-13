package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/tool"
)

// editTool replaces one unique string in a file.
type editTool struct{}

// editArgs are the parameters of an edit call.
type editArgs struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// Name identifies the tool to the model.
func (editTool) Name() string { return "edit" }

// Description tells the model what the tool does.
func (editTool) Description() string {
	return "Replace old_string with new_string in a file. old_string must appear exactly once, " +
		"so include enough surrounding lines to make it unique. Read the file first."
}

// Schema describes the parameters of an edit call.
func (editTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path relative to the workspace root."},
    "old_string": {"type": "string", "description": "Exact text to replace, including indentation."},
    "new_string": {"type": "string", "description": "Text to put in its place."}
  },
  "required": ["path", "old_string", "new_string"],
  "additionalProperties": false
}`)
}

// Call performs the replacement, refusing anything ambiguous.
func (editTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[editArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	switch {
	case args.Path == "":
		return tool.Errorf("edit: path is required"), nil
	case args.OldString == "":
		return tool.Errorf("edit %s: old_string is required; use write to create a file", args.Path), nil
	case args.OldString == args.NewString:
		return tool.Errorf("edit %s: old_string and new_string are identical", args.Path), nil
	}

	// The whole file is rewritten, so refuse one that does not fit in a
	// single read rather than truncating it.
	info, err := c.Exec.Stat(ctx, args.Path)
	if err != nil {
		return tool.Errorf("edit %s: %v", args.Path, err), nil
	}
	if info.IsDir {
		return tool.Errorf("edit %s: path is a directory", args.Path), nil
	}
	if info.Size > maxFileBytes {
		return tool.Errorf("edit %s: file is %d bytes, larger than the %d byte edit limit; change it with bash instead",
			args.Path, info.Size, maxFileBytes), nil
	}
	data, err := c.Exec.ReadFile(ctx, args.Path, executor.ReadOpts{MaxBytes: maxFileBytes})
	if err != nil {
		return tool.Errorf("edit %s: %v", args.Path, err), nil
	}
	content := string(data)
	switch n := strings.Count(content, args.OldString); {
	case n == 0:
		return tool.Errorf("edit %s: old_string was not found; read the file and copy the text exactly", args.Path), nil
	case n > 1:
		return tool.Errorf("edit %s: old_string appears %d times; include more surrounding lines to make it unique", args.Path, n), nil
	}

	updated := strings.Replace(content, args.OldString, args.NewString, 1)
	if err := c.Exec.WriteFile(ctx, args.Path, []byte(updated)); err != nil {
		return tool.Errorf("edit %s: %v", args.Path, err), nil
	}
	line := 1 + strings.Count(content[:strings.Index(content, args.OldString)], "\n")
	details, err := json.Marshal(map[string]any{"path": args.Path, "line": line})
	if err != nil {
		return tool.Result{}, fmt.Errorf("encode edit details: %w", err)
	}
	return tool.Result{
		Content: fmt.Sprintf("edited %s at line %d", args.Path, line),
		Details: details,
	}, nil
}

var _ tool.Tool = editTool{}
