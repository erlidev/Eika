package builtin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/tool"
)

// countLinesTool is the worked example from docs/EXTENDING.md: a tool that
// counts the lines of a file. Keeping it compiled here keeps the documentation
// honest.
type countLinesTool struct{}

// Name identifies the tool to the model.
func (countLinesTool) Name() string { return "count_lines" }

// Description tells the model what the tool does.
func (countLinesTool) Description() string { return "Count the lines of a text file." }

// Schema describes the parameters of a count_lines call.
func (countLinesTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path relative to the workspace root."}
  },
  "required": ["path"],
  "additionalProperties": false
}`)
}

// Call reads the file through the executor and counts its lines.
func (countLinesTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return tool.Errorf("count_lines: %v", err), nil
	}
	data, err := c.Exec.ReadFile(ctx, args.Path, executor.ReadOpts{MaxBytes: 1 << 20})
	if err != nil {
		return tool.Errorf("count_lines %s: %v", args.Path, err), nil
	}
	return tool.Text(fmt.Sprintf("%d", strings.Count(string(data), "\n"))), nil
}

// ExampleTool shows how a tool is written, registered, and called.
func ExampleTool() {
	registry, err := tool.NewRegistry(countLinesTool{})
	if err != nil {
		panic(err)
	}
	t, _ := registry.Get("count_lines")
	fmt.Println(t.Name(), t.Description())
	// Output: count_lines Count the lines of a text file.
}

func TestExampleToolCountsLines(t *testing.T) {
	w := newWorkspace(t)
	w.write("a.txt", "one\ntwo\nthree\n")
	if err := w.registry.Register(countLinesTool{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if res := w.call("count_lines", map[string]any{"path": "a.txt"}); res.Content != "3" {
		t.Errorf("result = %+v, want 3", res)
	}
}
