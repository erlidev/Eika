package server

import (
	"fmt"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

func TestRecoveryAnswersEveryMissingCallAndPreservesResults(t *testing.T) {
	for _, answered := range [][]string{nil, {"a"}, {"b"}, {"a", "b", "c"}} {
		t.Run("answered "+fmt.Sprint(answered), func(t *testing.T) {
			var path []store.Entry
			add := func(m provider.Message) {
				e, err := session.MessageEntry(m)
				if err != nil {
					t.Fatal(err)
				}
				path = append(path, e)
			}
			add(provider.AssistantMessage("working", []provider.ToolCall{{ID: "a"}, {ID: "b"}, {ID: "c"}}))
			for _, id := range answered {
				add(provider.ToolResultMessage(id, "original result", false))
			}
			recovered, err := unansweredResults(path)
			if err != nil {
				t.Fatal(err)
			}
			var got, want []string
			for _, id := range []string{"a", "b", "c"} {
				if !slices.Contains(answered, id) {
					want = append(want, id)
				}
			}
			for _, e := range recovered {
				m, ok, err := session.Message(e)
				if err != nil || !ok || m.Role != provider.RoleTool || !m.IsError || m.Content != restartedToolResult {
					t.Fatalf("recovered result = %+v, %v, %v", m, ok, err)
				}
				got = append(got, m.ToolCallID)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("result ids = %v, want %v", got, want)
			}
			if !session.PathResumable(append(path, recovered...)) {
				t.Fatal("repaired batch is not resumable")
			}
			again, err := unansweredResults(append(path, recovered...))
			if err != nil || len(again) != 0 {
				t.Fatalf("second recovery = %+v, %v", again, err)
			}
		})
	}
}

func TestRecoveryUsesTheCompactedConversation(t *testing.T) {
	call, err := session.MessageEntry(provider.AssistantMessage("", []provider.ToolCall{{ID: "old"}}))
	if err != nil {
		t.Fatal(err)
	}
	compact, err := session.CompactionEntry(agent.Compaction{Summary: "Old work is complete."})
	if err != nil {
		t.Fatal(err)
	}
	got, err := unansweredResults([]store.Entry{call, compact})
	if err != nil || len(got) != 0 {
		t.Fatalf("recovery = %+v, %v, want no old result", got, err)
	}
}

func TestReportNoticeIdentitySurvivesHeadChanges(t *testing.T) {
	child := store.Session{ID: "child", HeadEntryID: "last", Result: "done"}
	old := reportNoticeID(child)
	child.ReportNoticeID = old
	child.HeadEntryID = "repaired"
	if got := reportNoticeID(child); got != old {
		t.Fatalf("notice id = %q, want %q", got, old)
	}
	child.ReportNoticeID = "next-report"
	if reportNoticeID(child) == old {
		t.Fatal("new report reused the old id")
	}
}
