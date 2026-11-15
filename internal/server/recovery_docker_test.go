//go:build docker

package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
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

func TestRestartRepairsLegacyProfileAndChatSessions(t *testing.T) {
	a := newAPI(t)
	ctx := t.Context()
	profiles, err := a.store.Profiles(ctx)
	if err != nil || len(profiles) == 0 {
		t.Fatalf("profiles = %+v, %v", profiles, err)
	}
	computer := a.home(t)
	for _, c := range []struct {
		name, computerID string
		hasRun           bool
	}{
		{name: "profile session", computerID: computer.ID, hasRun: true},
		{name: "direct chat without a run row"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sess, err := a.store.CreateSession(ctx, store.Session{Title: c.name, ComputerID: c.computerID,
				ProfileID: profiles[0].ID, State: store.SessionRunning})
			if err != nil {
				t.Fatal(err)
			}
			messages := []provider.Message{
				provider.AssistantMessage("", []provider.ToolCall{{ID: "done", Name: "external"}, {ID: "lost", Name: "external"}}),
				provider.ToolResultMessage("done", "original", false),
			}
			var original []store.Entry
			for _, m := range messages {
				e, err := session.MessageEntry(m)
				if err != nil {
					t.Fatal(err)
				}
				saved, err := a.store.AppendEntry(ctx, sess.ID, e)
				if err != nil {
					t.Fatal(err)
				}
				original = append(original, saved)
			}
			var run store.Run
			if c.hasRun {
				run, err = a.store.StartRun(ctx, sess.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := a.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
			}
			path, err := a.store.SessionPath(ctx, sess.ID)
			if err != nil || len(path) != 3 || path[0].ID != original[0].ID || path[1].ID != original[1].ID || !session.PathResumable(path) {
				t.Fatalf("legacy path = %+v, %v; want original entries and a resumable result", path, err)
			}
			m, ok, err := session.Message(path[2])
			if err != nil || !ok || m.ToolCallID != "lost" || !m.IsError || m.Content != "Eika restarted while this ran; its outcome is unknown. Check before retrying." {
				t.Errorf("recovered result = %+v, %v, %v", m, ok, err)
			}
			now, err := a.store.Session(ctx, sess.ID)
			if err != nil || now.State != sess.State || now.ProfileID != sess.ProfileID {
				t.Errorf("legacy session = %+v, %v", now, err)
			}
			if c.hasRun {
				ended, err := a.store.Run(ctx, run.ID)
				if err != nil || ended.State != store.RunAborted {
					t.Errorf("legacy run = %+v, %v", ended, err)
				}
			}
		})
	}
}

func TestRestartPinsLegacyReportIDsBeforeRepairChangesTheHead(t *testing.T) {
	for _, posted := range []string{"missing", "current head"} {
		t.Run(posted, func(t *testing.T) {
			d := newDelegating(t)
			ctx := t.Context()
			child, err := d.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent, Agent: "researcher",
				Name: "old-child", ParentSessionID: d.home.ID, State: store.SessionRunning, Result: "saved report"})
			if err != nil {
				t.Fatal(err)
			}
			call, err := session.MessageEntry(provider.AssistantMessage("", []provider.ToolCall{{ID: "lost", Name: "external"}}))
			if err != nil {
				t.Fatal(err)
			}
			head, err := d.store.AppendEntry(ctx, child.ID, call)
			if err != nil {
				t.Fatal(err)
			}
			noticeHead := head.ID
			sum := sha256.Sum256([]byte("report\x00" + child.ID + "\x00" + noticeHead))
			noticeID := "notice-" + hex.EncodeToString(sum[:12])
			if posted != "missing" {
				notice, err := session.TextEntry(store.KindNotice, "old-child (researcher) reported: saved report")
				if err != nil {
					t.Fatal(err)
				}
				notice.ID = noticeID
				if _, _, err := d.store.AppendEntryOnce(ctx, d.home.ID, notice); err != nil {
					t.Fatal(err)
				}
			}
			for range 3 {
				if err := d.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
			}
			now, err := d.store.Session(ctx, child.ID)
			if err != nil || now.ReportNoticeID != noticeID || now.HeadEntryID == head.ID {
				t.Fatalf("recovered child = %+v, %v", now, err)
			}
			notices, _ := d.homeNotices(t)
			if len(notices) != 2 || notices[noticeID] != "old-child (researcher) reported: saved report" {
				t.Errorf("notices = %v, want one interruption and one report", notices)
			}
		})
	}
}

func TestRestartKeepsIdenticalLegacyReportsDistinct(t *testing.T) {
	d := newDelegating(t)
	ctx := t.Context()
	child, err := d.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent,
		ParentSessionID: d.home.ID, Agent: "researcher", Name: "worker", Result: "same report"})
	if err != nil {
		t.Fatal(err)
	}
	appendReport := func() store.Entry {
		e, err := session.MessageEntry(provider.AssistantMessage("same report", nil))
		if err != nil {
			t.Fatal(err)
		}
		saved, err := d.store.AppendEntry(ctx, child.ID, e)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	first := appendReport()
	firstHash := sha256.Sum256([]byte("report\x00" + child.ID + "\x00" + first.ID))
	firstID := "notice-" + hex.EncodeToString(firstHash[:12])
	notice, err := session.TextEntry(store.KindNotice, "worker (researcher) reported: same report")
	if err != nil {
		t.Fatal(err)
	}
	notice.ID = firstID
	if _, _, err := d.store.AppendEntryOnce(ctx, d.home.ID, notice); err != nil {
		t.Fatal(err)
	}
	delivered := provider.UserMessage("first report received")
	delivered.Notices = []string{firstID}
	user, err := session.MessageEntry(delivered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.AppendEntry(ctx, d.home.ID, user); err != nil {
		t.Fatal(err)
	}
	second := appendReport()
	secondHash := sha256.Sum256([]byte("report\x00" + child.ID + "\x00" + second.ID))
	secondID := "notice-" + hex.EncodeToString(secondHash[:12])
	for range 2 {
		if err := d.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	now, err := d.store.Session(ctx, child.ID)
	if err != nil || now.ReportNoticeID != secondID {
		t.Fatalf("report identity = %+v, %v", now, err)
	}
	texts, deliveries := d.homeNotices(t)
	if len(texts) != 2 || texts[firstID] != noticeTextForTest || texts[secondID] != noticeTextForTest || deliveries[firstID] != 1 || deliveries[secondID] != 0 {
		t.Fatalf("notices = %v, deliveries = %v; want old delivered and new waiting", texts, deliveries)
	}
	undelivered, err := d.store.UndeliveredNotices(ctx, d.home.ID)
	if err != nil || len(undelivered) != 1 || undelivered[0].ID != secondID {
		t.Fatalf("undelivered = %+v, %v", undelivered, err)
	}
}

const noticeTextForTest = "worker (researcher) reported: same report"

func TestStartupKeepsRecoveredNoticesUntilTheUserSendsAMessage(t *testing.T) {
	d := newDelegating(t)
	ctx := t.Context()
	child, err := d.store.CreateSession(ctx, store.Session{Kind: store.SessionSubagent,
		ParentSessionID: d.home.ID, Agent: "researcher", Name: "interrupted", State: store.SessionRunning})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.SetSubagentResult(ctx, child.ID, "saved report"); err != nil {
		t.Fatal(err)
	}
	old, err := session.TextEntry(store.KindNotice, "earlier notice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.AppendEntry(ctx, d.home.ID, old); err != nil {
		t.Fatal(err)
	}
	eika := providertest.New(providertest.Text("I found the notices."))
	d.use(byAgent{"You are Eika": eika})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.Listen = addr
	d.Server = server.New(cfg, testLogger(), d.deps, server.Options{})
	if err := d.BindAgentToolsForTest(d.host); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- d.Run(runCtx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("server run: %v", err)
		}
	}()
	client := &http.Client{Timeout: time.Second}
	waitFor(t, "server startup", func() bool {
		req, err := http.NewRequestWithContext(runCtx, "GET", "http://"+addr+"/healthz", nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	// Allow any incorrectly started background turn to reach the provider.
	select {
	case <-time.After(300 * time.Millisecond):
	case err := <-done:
		done <- err
		t.Fatalf("server stopped during startup: %v", err)
	}
	if got := d.runState(t, d.home.ID); got.Active || len(eika.Requests()) != 0 {
		t.Fatalf("startup started a home turn: %+v, requests %d", got, len(eika.Requests()))
	}
	runs, err := d.store.Runs(ctx, d.home.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("startup home runs = %+v, %v", runs, err)
	}
	notices, err := d.store.UndeliveredNotices(ctx, d.home.ID)
	if err != nil || len(notices) != 3 {
		t.Fatalf("startup notices = %+v, %v", notices, err)
	}
	d.postMessage(t, d.home.ID, "what happened?", "", 202)
	d.settled(t)
	reqs := eika.Requests()
	if len(reqs) != 1 || len(lastUser(reqs[0]).Notices) != 3 {
		t.Fatalf("user turn did not deliver the three notices: %+v", reqs)
	}
	updated, err := d.store.Session(ctx, child.ID)
	if err != nil || updated.State != store.SessionIdle {
		t.Fatalf("child resumed: %+v, %v", updated, err)
	}
}

func TestNewReportWakesHomeWithRecoveredNotices(t *testing.T) {
	d := newDelegating(t)
	ctx := t.Context()
	old, err := session.TextEntry(store.KindNotice, "recovered notice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.AppendEntry(ctx, d.home.ID, old); err != nil {
		t.Fatal(err)
	}
	if err := d.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	eika := providertest.New(providertest.Text("Both notices arrived."))
	d.use(byAgent{"You are Eika": eika})
	if err := d.PostNoticeForTest(ctx, d.home.ID, "new-report-notice", "new report"); err != nil {
		t.Fatal(err)
	}
	d.settled(t)
	reqs := eika.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want one background turn", len(reqs))
	}
	m := lastUser(reqs[0])
	if len(m.Notices) != 2 || !strings.Contains(m.Context, "recovered notice") || !strings.Contains(m.Context, "new report") {
		t.Errorf("background message = %+v, want both notices", m)
	}
}
