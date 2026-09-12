package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/tool"
)

// readTool returns the contents of a file with line numbers.
type readTool struct{}

// readArgs are the parameters of a read call.
type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// Name identifies the tool to the model.
func (readTool) Name() string { return "read" }

// Description tells the model what the tool does.
func (readTool) Description() string {
	return "Read a text file from the workspace. Output is line-numbered. " +
		"Use offset and limit to page through a file that is too large to read at once. " +
		"Binary files are refused."
}

// Schema describes the parameters of a read call.
func (readTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path relative to the workspace root."},
    "offset": {"type": "integer", "description": "First line to return, 1-based. Defaults to 1."},
    "limit": {"type": "integer", "description": "How many lines to return. Defaults to 2000."}
  },
  "required": ["path"],
  "additionalProperties": false
}`)
}

// Call reads the file and formats the requested line range.
func (readTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[readArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if args.Path == "" {
		return tool.Errorf("read: path is required"), nil
	}

	info, err := c.Exec.Stat(ctx, args.Path)
	if err != nil {
		return tool.Errorf("read %s: %v", args.Path, err), nil
	}
	if info.IsDir {
		return tool.Errorf("read %s: path is a directory, use ls", args.Path), nil
	}
	data, err := c.Exec.ReadFile(ctx, args.Path, executor.ReadOpts{MaxBytes: maxFileBytes})
	if err != nil {
		return tool.Errorf("read %s: %v", args.Path, err), nil
	}
	if isBinary(data) {
		return tool.Errorf("read %s: file is binary", args.Path), nil
	}

	if len(data) == 0 {
		return tool.Text(fmt.Sprintf("%s is empty", args.Path)), nil
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	offset := args.Offset
	if offset <= 0 {
		offset = 1
	}
	if offset > len(lines) {
		return tool.Errorf("read %s: offset %d is past the last line (%d lines)", args.Path, offset, len(lines)), nil
	}
	limit := limitOr(args.Limit, defaultReadLines)
	end := min(offset-1+limit, len(lines))

	var b strings.Builder
	for i := offset - 1; i < end; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, clipLine(lines[i]))
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "\n... %d more lines. Read again with offset %d.\n", len(lines)-end, end+1)
	}
	if int64(len(data)) < info.Size {
		fmt.Fprintf(&b, "\n... file is %d bytes; only the first %d were read.\n", info.Size, len(data))
	}
	return tool.Text(b.String()), nil
}

// isBinary reports whether data looks like something a model cannot read.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0
}

// clipLine shortens one very long line so that a minified file cannot fill the
// context window.
func clipLine(line string) string {
	if len(line) <= maxLineRunes {
		return line
	}
	runes := []rune(line)
	if len(runes) <= maxLineRunes {
		return line
	}
	return string(runes[:maxLineRunes]) + fmt.Sprintf("... [%d more characters]", len(runes)-maxLineRunes)
}

var _ tool.Tool = readTool{}
