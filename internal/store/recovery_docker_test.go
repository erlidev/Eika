//go:build docker

package store_test

import (
	"errors"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestRecoveryRollsBackWhenTheHomeNoticeCannotBeStored(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	home, err := st.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child, err := st.CreateSession(ctx, store.Session{Kind: store.SessionSubagent, Agent: "researcher",
		Name: "child", State: store.SessionWaiting, WaitingReason: "question", ParentSessionID: home.ID})
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.StartRun(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	repair := store.SessionRecovery{SessionID: child.ID,
		Entries:         []store.Entry{{Kind: store.KindToolResult, Payload: []byte(`{"role":"tool","tool_call_id":"call","content":"unknown"}`)}},
		NoticeSessionID: "missing-home", Notice: store.Entry{Kind: store.KindNotice, Payload: []byte(`{"text":"interrupted"}`)}}
	if err := st.RecoverSession(ctx, repair); err == nil {
		t.Fatal("recovery without a home succeeded")
	}
	now, err := st.Session(ctx, child.ID)
	if err != nil || now.HeadEntryID != "" || now.State != child.State || now.WaitingReason != child.WaitingReason {
		t.Fatalf("partial repair: %+v, %v", now, err)
	}
	active, err := st.Run(ctx, run.ID)
	if err != nil || active.State != store.RunRunning {
		t.Fatalf("run partially repaired: %+v, %v", active, err)
	}
	repair.NoticeSessionID = home.ID
	if err := st.RecoverSession(ctx, repair); err != nil {
		t.Fatal(err)
	}
	if err := st.RecoverSession(ctx, repair); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale head recovery = %v, want conflict", err)
	}
}

func TestReportNoticeIDIsStoredWithTheReportAndPinnedOnce(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	home, err := st.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child, err := st.CreateSession(ctx, store.Session{Kind: store.SessionSubagent, Agent: "researcher", Name: "child", ParentSessionID: home.ID, Result: "old report"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := st.PinReportNoticeID(ctx, child.ID, "original-notice")
	if err != nil || old.ReportNoticeID != "original-notice" {
		t.Fatalf("pin = %+v, %v", old, err)
	}
	again, err := st.PinReportNoticeID(ctx, child.ID, "changed-head-notice")
	if err != nil || again.ReportNoticeID != old.ReportNoticeID {
		t.Fatalf("pin changed identity: %+v, %v", again, err)
	}
	next, err := st.SetSubagentResult(ctx, child.ID, "same text")
	if err != nil || next.ReportNoticeID == "" || next.ReportNoticeID == old.ReportNoticeID {
		t.Fatalf("new report = %+v, %v", next, err)
	}
	latest, err := st.SetSubagentResult(ctx, child.ID, "same text")
	if err != nil || latest.ReportNoticeID == next.ReportNoticeID {
		t.Fatalf("repeated report = %+v, %v", latest, err)
	}
}
