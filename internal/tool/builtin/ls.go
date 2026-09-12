package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/tool"
)

// lsTool lists the contents of a directory.
type lsTool struct{}

// lsArgs are the parameters of an ls call.
type lsArgs struct {
	Path  string `json:"path"`
	Limit int    `json:"limit"`
}

// Name identifies the tool to the model.
func (lsTool) Name() string { return "ls" }

// Description tells the model what the tool does.
func (lsTool) Description() string {
	return "List the direct contents of a directory. Directories are listed first and marked with a " +
		"trailing slash; files show their size in bytes."
}

// Schema describes the parameters of an ls call.
func (lsTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Directory relative to the workspace root. Defaults to the root."},
    "limit": {"type": "integer", "description": "Maximum number of entries to return. Defaults to 500."}
  },
  "additionalProperties": false
}`)
}

// Call lists the directory.
func (lsTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[lsArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	path := pathOrDot(args.Path)
	entries, err := c.Exec.List(ctx, path)
	if err != nil {
		return tool.Errorf("ls %s: %v", path, err), nil
	}
	if len(entries) == 0 {
		return tool.Text(fmt.Sprintf("%s is empty", path)), nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})

	limit := limitOr(args.Limit, defaultListLimit)
	var b strings.Builder
	for i, e := range entries {
		if i == limit {
			fmt.Fprintf(&b, "\n... %d more entries.\n", len(entries)-limit)
			break
		}
		b.WriteString(describe(e))
		b.WriteByte('\n')
	}
	return tool.Text(strings.TrimRight(b.String(), "\n")), nil
}

// describe renders one directory entry.
func describe(e executor.FileInfo) string {
	if e.IsDir {
		return e.Name + "/"
	}
	return fmt.Sprintf("%s (%d bytes)", e.Name, e.Size)
}

var _ tool.Tool = lsTool{}
