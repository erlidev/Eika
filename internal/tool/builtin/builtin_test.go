package builtin_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// computer is a temporary computer with the built-in tools wired to it.
type computer struct {
	t        *testing.T
	exec     *local.Executor
	registry *tool.Registry
	outputs  []string
}

// newComputer returns a computer rooted at a fresh temporary directory.
func newComputer(t *testing.T) *computer {
	t.Helper()
	e, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	r, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	return &computer{t: t, exec: e, registry: r}
}

// write puts a file in the computer.
func (w *computer) write(path, content string) {
	w.t.Helper()
	if err := w.exec.WriteFile(context.Background(), path, []byte(content)); err != nil {
		w.t.Fatalf("write %s: %v", path, err)
	}
}

// call runs one tool with the given arguments.
func (w *computer) call(name string, args any) tool.Result {
	w.t.Helper()
	tl, ok := w.registry.Get(name)
	if !ok {
		w.t.Fatalf("tool %s is not registered", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		w.t.Fatalf("encode arguments: %v", err)
	}
	res, err := tl.Call(context.Background(), tool.CallContext{
		Exec:      w.exec,
		Emit:      event.EmitterFunc(w.record),
		SessionID: "session-1",
		RunID:     "run-1",
		CallID:    "call-1",
	}, raw)
	if err != nil {
		w.t.Fatalf("%s: %v", name, err)
	}
	return res
}

// record collects tool.output events so a test can assert on streaming.
func (w *computer) record(_ context.Context, e event.Event) {
	if e.Type != event.TypeToolOutput {
		return
	}
	var payload event.ToolOutput
	if err := e.DecodePayload(&payload); err != nil {
		w.t.Errorf("decode tool.output payload: %v", err)
		return
	}
	w.outputs = append(w.outputs, payload.Text)
}

func TestRegistryHoldsEveryBuiltinTool(t *testing.T) {
	r, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	want := []string{
		"ask_user", "bash", "file_edit", "file_read", "file_write", "project_create", "project_list", "project_open", "project_update", "web_fetch", "web_search",
	}
	got := make([]string, 0, len(want))
	for _, tl := range r.List() {
		got = append(got, tl.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tools = %v, want %v", got, want)
		}
	}
	for _, def := range r.Schemas() {
		if def.Description == "" {
			t.Errorf("tool %s has no description", def.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(def.Schema, &schema); err != nil {
			t.Errorf("tool %s has an unparseable schema: %v", def.Name, err)
		}
		if schema["type"] != "object" {
			t.Errorf("tool %s schema type = %v, want object", def.Name, schema["type"])
		}
	}
}

// A chat offers the standalone tools alone. Every tool that reads or changes
// files or runs a command needs explicit access through its dependencies.
func TestOnlyToolsThatNeedNoComputerAreStandalone(t *testing.T) {
	r, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	var standalone []string
	for _, tl := range r.List() {
		if !tool.NeedsComputer(tl) {
			standalone = append(standalone, tl.Name())
		}
	}
	want := []string{"ask_user", "project_create", "project_list", "project_open", "project_update", "web_fetch", "web_search"}
	if !slices.Equal(standalone, want) {
		t.Errorf("standalone tools = %v, want %v", standalone, want)
	}
}
