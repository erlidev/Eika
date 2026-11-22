package server

import (
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// A pinned tool list can only narrow the definition's. A subagent whose
// snapshot names memory tools, or whose stored list is anything at all, still
// gets none, and an old Eika snapshot gets only what it pinned.
func TestRunToolsNeverGiveSubagentsMemory(t *testing.T) {
	reg, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{deps: Deps{Tools: reg}}
	memory := []string{"memory_forget", "memory_read", "memory_search", "memory_write"}
	for _, agent := range []string{"researcher", "worker"} {
		sess := store.Session{Agent: agent, ComputerID: "c"}
		for _, name := range s.sessionToolNames(sess, append([]string{"report", "web_search"}, memory...)) {
			if slices.Contains(memory, name) {
				t.Errorf("%s was offered %s", agent, name)
			}
		}
	}
	eika := store.Session{Agent: "eika"}
	got := s.sessionToolNames(eika, []string{"project_list", "memory_search"})
	if !slices.Equal(got, []string{"memory_search", "project_list"}) {
		t.Errorf("eika from an old pin = %v", got)
	}
	if got := s.sessionToolNames(eika, nil); !slices.Equal(slices.DeleteFunc(slices.Clone(got), func(n string) bool { return !slices.Contains(memory, n) }), memory) {
		t.Errorf("eika from the definition = %v", got)
	}
}

// skill_use reaches Eika and the worker through their definitions, and a
// pinned list from before it existed hides it rather than gaining it.
func TestRunToolsSkillUseIntersectsPins(t *testing.T) {
	reg, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{deps: Deps{Tools: reg}}
	for agent, want := range map[string]bool{"eika": true, "worker": true, "researcher": false} {
		sess := store.Session{Agent: agent, ComputerID: "c"}
		if got := slices.Contains(s.sessionToolNames(sess, nil), "skill_use"); got != want {
			t.Errorf("%s offered skill_use = %v", agent, got)
		}
		if slices.Contains(s.sessionToolNames(sess, []string{"report", "web_search"}), "skill_use") {
			t.Errorf("%s: an old pin gained skill_use", agent)
		}
	}
}
