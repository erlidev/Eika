package server

import (
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

func TestPreambleIsStableAndListsOnlyActiveProjects(t *testing.T) {
	d := preambleData{global: store.GlobalInstructions{AboutMe: "About", Rules: "Rules"}, projectInstructions: "Project rules", home: true, zone: "UTC", locale: "en-US", projects: []store.Project{
		{Slug: "z", ProjectFields: store.ProjectFields{Name: "Z", Status: "active", Description: "Last"}},
		{Slug: "hidden", ProjectFields: store.ProjectFields{Name: "Hidden", Status: "done"}},
		{Slug: "a", ProjectFields: store.ProjectFields{Name: "A", Status: "active", Description: "First"}},
	}}
	got := renderPreamble(d)
	if again := renderPreamble(d); again != got {
		t.Fatalf("preamble changed between renders: %q, then %q", got, again)
	}
	first, last := strings.Index(got, "- A (a): First"), strings.Index(got, "- Z (z): Last")
	if first < 0 || last < first {
		t.Fatalf("preamble = %q, want the active projects sorted by slug", got)
	}
	if strings.Contains(got, "Hidden") {
		t.Fatal("inactive project included")
	}
}

func TestCompactionBranchReplaysReplacementPreamble(t *testing.T) {
	old, err := session.TextEntry(store.KindPreamble, "old")
	if err != nil {
		t.Fatal(err)
	}
	user, err := session.MessageEntry(provider.UserMessage("earlier"))
	if err != nil {
		t.Fatal(err)
	}
	added, err := preambleCompactionEntries("new", "You are Eika.", []string{"project_list"}, agent.Compaction{Summary: "summary"})
	if err != nil {
		t.Fatal(err)
	}
	path := append([]store.Entry{old, user}, added...)
	if path[len(path)-1].Kind != store.KindCompaction || !session.PathResumable(path) {
		t.Fatalf("compaction is not a branch point: %+v", path)
	}
	snap, err := session.SnapshotOf(path)
	if err != nil || snap.Text != "new" || snap.Prompt == nil || *snap.Prompt != "You are Eika." ||
		!slices.Equal(snap.Tools, []string{"project_list"}) {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
	messages, err := session.Messages(path)
	if err != nil || len(messages) != 1 || !messages[0].Summary {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
}
