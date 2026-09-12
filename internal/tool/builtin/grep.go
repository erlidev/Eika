package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/tool"
)

// grepTool searches file contents for a pattern.
type grepTool struct{}

// grepArgs are the parameters of a grep call.
type grepArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	IgnoreCase bool   `json:"ignore_case"`
	Context    int    `json:"context"`
	Limit      int    `json:"limit"`
}

// Name identifies the tool to the model.
func (grepTool) Name() string { return "grep" }

// Description tells the model what the tool does.
func (grepTool) Description() string {
	return "Search file contents for a regular expression. Returns matching lines with their file " +
		"and line number. Uses ripgrep when the workspace has it and grep otherwise."
}

// Schema describes the parameters of a grep call.
func (grepTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Regular expression to search for."},
    "path": {"type": "string", "description": "File or directory to search. Defaults to the workspace root."},
    "glob": {"type": "string", "description": "Only search files matching this glob, for example *.go."},
    "ignore_case": {"type": "boolean", "description": "Match case-insensitively."},
    "context": {"type": "integer", "description": "Lines of context to show around each match."},
    "limit": {"type": "integer", "description": "Maximum number of result lines. Defaults to 100."}
  },
  "required": ["pattern"],
  "additionalProperties": false
}`)
}

// Call runs the search, preferring ripgrep and falling back to grep.
func (grepTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	args, err := decodeArgs[grepArgs](raw)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if args.Pattern == "" {
		return tool.Errorf("grep: pattern is required"), nil
	}
	path := pathOrDot(args.Path)
	limit := limitOr(args.Limit, defaultMatchLimit)

	out, res, err := runSearch(ctx, c, "rg", ripgrepArgs(args, path))
	if err != nil || res.ExitCode == 127 {
		out, res, err = runSearch(ctx, c, "grep", posixGrepArgs(args, path))
		if err != nil {
			return tool.Errorf("grep: %v", err), nil
		}
	}
	if res.TimedOut {
		return tool.Errorf("grep: search timed out"), nil
	}
	text := strings.TrimRight(out.String(), "\n")
	if text == "" {
		return tool.Text(fmt.Sprintf("no matches for %q in %s", args.Pattern, path)), nil
	}
	return tool.Text(limitLines(text, limit)), nil
}

// ripgrepArgs builds the ripgrep invocation.
func ripgrepArgs(a grepArgs, path string) []string {
	args := []string{"--line-number", "--no-heading", "--color", "never", "--max-columns", "400"}
	if a.IgnoreCase {
		args = append(args, "--ignore-case")
	}
	if a.Context > 0 {
		args = append(args, "--context", strconv.Itoa(a.Context))
	}
	if a.Glob != "" {
		args = append(args, "--glob", a.Glob)
	}
	return append(args, "--regexp", a.Pattern, path)
}

// posixGrepArgs builds the POSIX grep invocation used when ripgrep is missing.
func posixGrepArgs(a grepArgs, path string) []string {
	args := []string{"-r", "-n", "-E", "--binary-files=without-match", "--exclude-dir=.git"}
	if a.IgnoreCase {
		args = append(args, "-i")
	}
	if a.Context > 0 {
		args = append(args, "-C", strconv.Itoa(a.Context))
	}
	if a.Glob != "" {
		args = append(args, "--include="+a.Glob)
	}
	return append(args, "-e", a.Pattern, path)
}

// runSearch executes one search command and captures its output.
func runSearch(ctx context.Context, c tool.CallContext, command string, args []string) (*output, executor.ExecResult, error) {
	out := &output{}
	res, err := c.Exec.Exec(ctx, executor.ExecSpec{
		Command: command,
		Args:    args,
		Timeout: defaultBashTimeout,
		Stdout:  out,
		Stderr:  out,
	})
	return out, res, err
}

// limitLines keeps at most n lines and says how many were dropped.
func limitLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n\n... %d more results. Narrow the search.", len(lines)-n)
}

var _ tool.Tool = grepTool{}
