package agentdef_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/tool/builtin"
)

func TestBuiltins(t *testing.T) {
	registry, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, d := range agentdef.Builtins() {
		names = append(names, d.Name)
		if d.Prompt == "" || d.Description == "" || d.Compaction.Summary == "" || d.Compaction.Update == "" || d.Compaction.TurnPrefix == "" {
			t.Errorf("missing embedded text for %s", d.Name)
		}
		for _, name := range d.Tools {
			if _, ok := registry.Get(name); !ok {
				t.Errorf("%s has unshipped tool %s", d.Name, name)
			}
		}
		for _, name := range []string{"file_edit", "file_read", "file_write"} {
			if slices.Contains(d.Tools, name) != (d.Name == "worker") {
				t.Errorf("%s file capability %s is incorrect", d.Name, name)
			}
		}
		// Eika delegates; subagents never do.
		for _, name := range []string{"subagent_cancel", "subagent_message", "subagent_start", "subagent_status"} {
			if slices.Contains(d.Tools, name) != (d.Name == "eika") {
				t.Errorf("%s delegation capability %s is incorrect", d.Name, name)
			}
		}
		// Only a subagent asks Eika a question.
		if slices.Contains(d.Tools, "ask_user") != (d.Name != "eika") {
			t.Errorf("%s question capability is incorrect", d.Name)
		}
		// Only a subagent reports; Eika reads the reports.
		if slices.Contains(d.Tools, "report") != (d.Name != "eika") {
			t.Errorf("%s report capability is incorrect", d.Name)
		}
		// Only Eika reads and writes memory.
		for _, name := range []string{"memory_forget", "memory_read", "memory_search", "memory_write"} {
			if slices.Contains(d.Tools, name) != (d.Name == "eika") {
				t.Errorf("%s memory capability %s is incorrect", d.Name, name)
			}
		}
		if d.Name != "worker" && slices.Contains(d.Tools, "bash") {
			t.Errorf("%s has computer access", d.Name)
		}
	}
	if !slices.Equal(names, []string{"eika", "researcher", "worker"}) {
		t.Fatal(names)
	}
	d, _ := agentdef.Lookup("worker")
	d.Tools[0] = "changed"
	fresh, _ := agentdef.Lookup("worker")
	if fresh.Tools[0] == "changed" {
		t.Fatal("lookup shares mutable tools")
	}
}

func TestOverride(t *testing.T) {
	d, _ := agentdef.Lookup("eika")
	got := agentdef.Override{Class: new("deep"), Prompt: new("")}.Apply(d)
	if got.Class != "deep" || got.Prompt != "" || !slices.Equal(got.Tools, d.Tools) {
		t.Fatalf("Apply = %+v", got)
	}
	if got := (agentdef.Override{}).Apply(d); got.Prompt != d.Prompt || got.Class != d.Class {
		t.Fatal("an empty override changed the definition")
	}
	for name, o := range map[string]agentdef.Override{
		"unknown class": {Class: new("decision")},
		"long prompt":   {Prompt: new(strings.Repeat("x", 64<<10+1))},
		"NUL":           {Prompt: new("before\x00after")},
	} {
		if o.Validate() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
