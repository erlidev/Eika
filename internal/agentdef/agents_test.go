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
		if !slices.IsSorted(d.Tools) {
			t.Errorf("unsorted tools: %v", d.Tools)
		}
		for i := 1; i < len(d.Tools); i++ {
			if d.Tools[i] == d.Tools[i-1] {
				t.Errorf("%s has duplicate tool %s", d.Name, d.Tools[i])
			}
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
		wantTurns, wantClass := 200, "main"
		if d.Name == "eika" {
			wantTurns = 40
			if slices.Contains(d.Tools, "bash") || slices.Contains(d.Tools, "ask_user") {
				t.Error("home has worker tools")
			}
		}
		if d.Name == "researcher" {
			wantClass = "fast"
			if slices.Contains(d.Tools, "bash") {
				t.Error("researcher has computer access")
			}
		}
		if d.MaxTurns != wantTurns || d.Class != wantClass {
			t.Errorf("defaults: %+v", d)
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
	if _, ok := agentdef.Lookup("browser"); ok {
		t.Fatal("deferred agent exists")
	}
}

func TestOverride(t *testing.T) {
	d, _ := agentdef.Lookup("eika")
	for _, class := range []string{"main", "fast", "deep", "decision", "", "unknown"} {
		t.Run(class, func(t *testing.T) {
			o := agentdef.Override{Class: &class, Prompt: new("")}
			valid := class == "main" || class == "fast" || class == "deep"
			if (o.Validate() == nil) != valid {
				t.Fatalf("Validate = %v", o.Validate())
			}
			if valid {
				got := o.Apply(d)
				if got.Class != class || got.Prompt != "" || !slices.Equal(got.Tools, d.Tools) {
					t.Fatalf("Apply = %+v", got)
				}
			}
		})
	}
	if got := (agentdef.Override{}).Apply(d); got.Prompt != d.Prompt || got.Class != d.Class {
		t.Fatal("nil did not fall back")
	}
	for _, n := range []int{65536, 65537} {
		o := agentdef.Override{Prompt: new(strings.Repeat("x", n))}
		if (o.Validate() == nil) != (n == 65536) {
			t.Errorf("length %d: %v", n, o.Validate())
		}
	}
	for _, prompt := range []string{"\x00", "before\x00after"} {
		o := agentdef.Override{Prompt: &prompt}
		if err := o.Validate(); err == nil {
			t.Errorf("prompt %q with NUL was accepted", prompt)
		}
	}
}
