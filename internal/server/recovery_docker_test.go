//go:build docker

package server_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

func TestRestartRepairsChildrenAndHomeWithoutExecutingTools(t *testing.T) {
	d := newDelegating(t)
	ctx := t.Context()
	var ids []string
	for _, state := range []store.SessionState{store.SessionRunning, store.SessionWaiting, store.SessionIdle} {
		reason := ""
		if state == store.SessionWaiting {
			reason = "a question"
		}
		child, err := d.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent,
			ParentSessionID: d.home.ID, Agent: "researcher", Name: string(state), State: state, WaitingReason: reason})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, child.ID)
	}
	ids = append(ids, d.home.ID)
	var runIDs []string
	for _, id := range ids {
		run, err := d.store.StartRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		runIDs = append(runIDs, run.ID)
		messages := []provider.Message{
			provider.UserMessage("do work"),
			provider.AssistantMessage("", []provider.ToolCall{{ID: "first", Name: "external"}, {ID: "second", Name: "external"}, {ID: "third", Name: "external"}}),
			provider.ToolResultMessage("second", "already ran", false),
		}
		for _, m := range messages {
			e, err := session.MessageEntry(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.store.AppendEntry(ctx, id, e); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A child can die between creation and StartRun.
	orphan, err := d.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent,
		ParentSessionID: d.home.ID, Agent: "researcher", Name: "orphan", State: store.SessionRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range append(ids[:len(ids):len(ids)], orphan.ID) {
		sess, err := d.store.Session(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if sess.State != store.SessionIdle || sess.WaitingReason != "" {
			t.Errorf("session = %+v, want idle without a wait", sess)
		}
		if id == orphan.ID {
			continue
		}
		path, err := d.store.SessionPath(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !session.PathResumable(path) {
			t.Fatal("recovered path is not resumable")
		}
		messages, err := session.Messages(path)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, m := range messages {
			if m.Role != provider.RoleTool {
				continue
			}
			counts[m.ToolCallID]++
			if m.ToolCallID == "second" {
				if m.Content != "already ran" || m.IsError {
					t.Errorf("original result changed: %+v", m)
				}
			} else if !m.IsError || m.Content != "Eika restarted while this ran; its outcome is unknown. Check before retrying." {
				t.Errorf("missing restart error: %+v", m)
			}
		}
		if len(counts) != 3 || counts["first"] != 1 || counts["second"] != 1 || counts["third"] != 1 {
			t.Errorf("result counts = %v", counts)
		}
	}
	for _, id := range runIDs {
		run, err := d.store.Run(ctx, id)
		if err != nil || run.State != store.RunAborted || run.FinishedAt.IsZero() {
			t.Errorf("run = %+v, %v", run, err)
		}
	}
	before, _ := d.homeNotices(t)
	if len(before) != 5 {
		t.Fatalf("notices = %v, want five interruptions", before)
	}
	for range 2 {
		if err := d.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := d.homeNotices(t)
	if len(after) != len(before) {
		t.Fatalf("repeated recovery added notices: %v", after)
	}
	eika := providertest.New(providertest.Text("Work was interrupted."))
	d.use(byAgent{"You are Eika": eika})
	if got := d.runState(t, d.home.ID); got.Active || len(eika.Requests()) != 0 {
		t.Fatalf("home started during recovery: %+v", got)
	}
	d.postMessage(t, d.home.ID, "what happened?", "", 202)
	d.settled(t)
	if len(eika.Requests()) != 1 || len(lastUser(eika.Requests()[0]).Notices) != 5 {
		t.Errorf("the next user turn did not receive the five notices")
	}
	for _, id := range ids[:3] {
		runs, err := d.store.Runs(ctx, id)
		if err != nil || len(runs) != 1 {
			t.Errorf("child resumed: runs = %+v, %v", runs, err)
		}
	}
}

func TestRestartFindsStoredReportsAndUndeliveredNotices(t *testing.T) {
	d := newDelegating(t)
	ctx := t.Context()
	for _, name := range []string{"idle-before-notice", "failed-write", "already-delivered"} {
		child, err := d.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent,
			ParentSessionID: d.home.ID, Agent: "researcher", Name: name})
		if err != nil {
			t.Fatal(err)
		}
		child, err = d.store.SetSubagentResult(ctx, child.ID, name+" report")
		if err != nil {
			t.Fatal(err)
		}
		if name == "already-delivered" {
			e, err := session.TextEntry(store.KindNotice, name+" report")
			if err != nil {
				t.Fatal(err)
			}
			e.ID = child.ReportNoticeID
			if _, _, err := d.store.AppendEntryOnce(ctx, d.home.ID, e); err != nil {
				t.Fatal(err)
			}
			m := provider.UserMessage("earlier turn")
			m.Notices = []string{e.ID}
			user, err := session.MessageEntry(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.store.AppendEntry(ctx, d.home.ID, user); err != nil {
				t.Fatal(err)
			}
		}
	}
	e, err := session.TextEntry(store.KindNotice, "stored before restart")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.AppendEntry(ctx, d.home.ID, e); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := d.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	eika := providertest.New(providertest.Text("Here are the reports."))
	d.use(byAgent{"You are Eika": eika})
	if got := d.runState(t, d.home.ID); got.Active || len(eika.Requests()) != 0 {
		t.Fatalf("home started during recovery: %+v", got)
	}
	d.postMessage(t, d.home.ID, "what is new?", "", 202)
	d.settled(t)
	reqs := eika.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want one user turn", len(reqs))
	}
	m := lastUser(reqs[0])
	if len(m.Notices) != 3 || strings.Contains(m.Context, "already-delivered") {
		t.Errorf("user message = %+v", m)
	}
	texts, delivered := d.homeNotices(t)
	if len(texts) != 4 {
		t.Errorf("notices = %v, want four", texts)
	}
	for id := range texts {
		if delivered[id] != 1 {
			t.Errorf("deliveries = %v, want each once", delivered)
		}
	}
}

func TestRestartDoesNotRepeatAReportDeliveredBeforeResume(t *testing.T) {
	d := newDelegating(t)
	ctx := t.Context()
	child, err := d.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent,
		ParentSessionID: d.home.ID, Agent: "researcher", Name: "child", Result: "report"})
	if err != nil {
		t.Fatal(err)
	}
	appendMessage := func(m provider.Message) store.Entry {
		e, err := session.MessageEntry(m)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := d.store.AppendEntry(ctx, child.ID, e)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	appendMessage(provider.AssistantMessage("", []provider.ToolCall{{ID: "report", Name: "report"}}))
	appendMessage(provider.ToolResultMessage("report", "stored", false))
	appendMessage(provider.AssistantMessage("done", nil))
	child, err = d.store.SetSubagentResult(ctx, child.ID, "report")
	if err != nil {
		t.Fatal(err)
	}
	id := child.ReportNoticeID
	notice, err := session.TextEntry(store.KindNotice, "child (researcher) reported: report")
	if err != nil {
		t.Fatal(err)
	}
	notice.ID = id
	if _, _, err := d.store.AppendEntryOnce(ctx, d.home.ID, notice); err != nil {
		t.Fatal(err)
	}
	message := provider.UserMessage("received")
	message.Notices = []string{id}
	user, err := session.MessageEntry(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.AppendEntry(ctx, d.home.ID, user); err != nil {
		t.Fatal(err)
	}
	appendMessage(provider.UserMessage("continue"))
	appendMessage(provider.AssistantMessage("", []provider.ToolCall{{ID: "lost", Name: "external"}}))
	if _, err := d.store.SetSubagentState(ctx, child.ID, store.SessionRunning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.StartRun(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := d.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	texts, deliveries := d.homeNotices(t)
	if len(texts) != 2 || texts[id] != "child (researcher) reported: report" || deliveries[id] != 1 {
		t.Fatalf("notices = %v, deliveries = %v; want the old report and one interruption", texts, deliveries)
	}
	stored, err := d.store.Session(ctx, child.ID)
	if err != nil || stored.ReportNoticeID != id || stored.State != store.SessionIdle {
		t.Fatalf("child = %+v, %v", stored, err)
	}
}
