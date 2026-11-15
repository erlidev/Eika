package server

import (
	"fmt"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
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

func TestLegacyReportIdentityUsesTheReportAssignment(t *testing.T) {
	child := store.Session{ID: "child", Result: "same"}
	entry := func(id string, m provider.Message) store.Entry {
		e, err := session.MessageEntry(m)
		if err != nil {
			t.Fatal(err)
		}
		e.ID = id
		return e
	}
	oldCall := entry("old-call", provider.AssistantMessage("", []provider.ToolCall{{ID: "old", Name: "report"}}))
	oldResult := entry("old-result", provider.ToolResultMessage("old", "ok", false))
	resume := entry("resume", provider.UserMessage("do more"))
	child.HeadEntryID = oldResult.ID
	oldID := reportNoticeID(child)
	oldNotice := store.Entry{ID: oldID, Kind: store.KindNotice}
	path := []store.Entry{oldCall, oldResult, resume}
	child.HeadEntryID = resume.ID
	id, err := legacyReportNoticeID(child, path, []store.Entry{oldNotice})
	if err != nil || id != oldID {
		t.Fatalf("resumed report id = %q, %v, want %q", id, err, oldID)
	}
	newCall := entry("new-call", provider.AssistantMessage("", []provider.ToolCall{{ID: "new", Name: "report"}}))
	newResult := entry("new-result", provider.ToolResultMessage("new", "ok", false))
	path = append(path, newCall, newResult)
	child.HeadEntryID = newCall.ID
	newID, err := legacyReportNoticeID(child, path, []store.Entry{oldNotice})
	if err != nil || newID != reportNoticeID(child) || newID == oldID {
		t.Fatalf("new report id = %q, %v", newID, err)
	}
}

func TestUnanchoredLegacyReportUsesOneStableRecoveryID(t *testing.T) {
	child := store.Session{ID: "child", HeadEntryID: "one", Result: "report"}
	first, err := legacyReportNoticeID(child, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	child.HeadEntryID = "two"
	second, err := legacyReportNoticeID(child, nil, nil)
	if err != nil || second != first {
		t.Fatalf("legacy id after resume = %q, %v, want %q", second, err, first)
	}
}

func TestUnanchoredLegacyReportReusesAPreviousNotice(t *testing.T) {
	child := store.Session{ID: "child", HeadEntryID: "old", Result: "report"}
	id := reportNoticeID(child)
	old, err := session.MessageEntry(provider.AssistantMessage("work", nil))
	if err != nil {
		t.Fatal(err)
	}
	old.ID = "old"
	resume, err := session.MessageEntry(provider.UserMessage("continue"))
	if err != nil {
		t.Fatal(err)
	}
	resume.ID = "new"
	child.HeadEntryID = resume.ID
	got, err := legacyReportNoticeID(child, []store.Entry{old, resume}, []store.Entry{{ID: id, Kind: store.KindNotice}})
	if err != nil || got != id {
		t.Fatalf("notice id = %q, %v, want %q", got, err, id)
	}
}

func TestUnfinishedReportCallDoesNotReplaceTheLastLegacyReport(t *testing.T) {
	child := store.Session{ID: "child", State: store.SessionRunning, Result: "earlier report"}
	entry := func(id string, m provider.Message) store.Entry {
		e, err := session.MessageEntry(m)
		if err != nil {
			t.Fatal(err)
		}
		e.ID = id
		return e
	}
	oldCall := entry("old-call", provider.AssistantMessage("", []provider.ToolCall{{ID: "old", Name: "report"}}))
	oldResult := entry("old-result", provider.ToolResultMessage("old", "ok", false))
	child.HeadEntryID = oldResult.ID
	oldID := reportNoticeID(child)
	resume := entry("resume", provider.UserMessage("continue"))
	unanswered := entry("new-call", provider.AssistantMessage("", []provider.ToolCall{{ID: "new", Name: "report"}}))
	path := []store.Entry{oldCall, oldResult, resume, unanswered}
	child.HeadEntryID = unanswered.ID
	got, err := legacyReportNoticeID(child, path, []store.Entry{{ID: oldID, Kind: store.KindNotice}})
	if err != nil || got != oldID {
		t.Fatalf("id = %q, %v, want earlier report %q", got, err, oldID)
	}
}

func TestLegacyFallbackReportKeepsItsNoticeAfterResume(t *testing.T) {
	child := store.Session{ID: "child", State: store.SessionRunning, Result: "fallback"}
	entry := func(id string, m provider.Message) store.Entry {
		e, err := session.MessageEntry(m)
		if err != nil {
			t.Fatal(err)
		}
		e.ID = id
		return e
	}
	draft := entry("draft", provider.UserMessage(subagent.DraftReport))
	final := entry("final", provider.AssistantMessage("fallback", nil))
	resume := entry("resume", provider.UserMessage("more work"))
	child.HeadEntryID = final.ID
	id := reportNoticeID(child)
	child.HeadEntryID = resume.ID
	got, err := legacyReportNoticeID(child, []store.Entry{draft, final, resume}, []store.Entry{{ID: id, Kind: store.KindNotice}})
	if err != nil || got != id {
		t.Fatalf("notice id = %q, %v, want %q", got, err, id)
	}
}
