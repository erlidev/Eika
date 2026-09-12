package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/erlidev/eika/internal/tool"
)

// findTool lists files whose path matches a glob.
type findTool struct{}

// findArgs are the parameters of a find call.
type findArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Limit   int    `json:"limit"`
}

// Name identifies the tool to the model.
func (findTool) Name() string { return "find" }

// Description tells the model what the tool does.
func (findTool) Description() string {
	return "List files whose path matches a glob, for example **/*.go or Makefile. " +
		"Searches the workspace root unless a path is given."
}

// Schema describes the parameters of a find call.
func (findTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Glob the file path must match, for example **/*.go."},
    "path": {"type": "string", "description": "Directory to search. Defaults to the workspace root."},
    "limit": {"type": "integer", "description": "Maximum number of paths to return. Defaults to 200."}
  },
  "required": ["pattern"],
  "additionalProperties": false
}`)
}

// Call runs the search, preferring ripgrep's file listing and falling back to
// find.
func (findTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[findArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if args.Pattern == "" {
		return tool.Errorf("find: pattern is required"), nil
	}
	path := pathOrDot(args.Path)

	out, res, err := runSearch(ctx, c, "rg", []string{"--files", "--hidden", "--glob", "!.git", "--glob", args.Pattern, path})
	if err != nil || res.ExitCode == 127 {
		out, res, err = runSearch(ctx, c, "find", findCommandArgs(args.Pattern, path))
		if err != nil {
			return tool.Errorf("find: %v", err), nil
		}
	}
	if res.TimedOut {
		return tool.Errorf("find: search timed out"), nil
	}
	text := strings.TrimRight(out.String(), "\n")
	if text == "" {
		return tool.Text(fmt.Sprintf("no files matching %q under %s", args.Pattern, path)), nil
	}
	return tool.Text(limitLines(text, limitOr(args.Limit, defaultFindLimit))), nil
}

// findCommandArgs translates a glob into a find invocation. A pattern without
// a separator matches the base name; anything else matches the whole path.
func findCommandArgs(pattern, path string) []string {
	args := []string{path, "-type", "f", "-not", "-path", "*/.git/*"}
	trimmed := strings.TrimPrefix(pattern, "**/")
	if strings.Contains(trimmed, "/") {
		return append(args, "-path", "*"+trimmed)
	}
	return append(args, "-name", trimmed)
}

var _ tool.Tool = findTool{}
