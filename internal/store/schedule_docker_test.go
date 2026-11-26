//go:build docker

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/schedule"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/jackc/pgx/v5"
)

func TestWakeupLimit(t *testing.T) {
	st := storetest.Open(t)
	sess, err := st.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	w := schedule.Wakeup{SessionID: sess.ID, Agent: sess.Agent, Note: "check", DueAt: time.Now().Add(time.Hour)}
	results := make(chan error, 24)
	for range 24 {
		go func() { _, err := st.CreateWakeup(t.Context(), w); results <- err }()
	}
	success, limited := 0, 0
	for range 24 {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, store.ErrWakeupLimit):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if success != 20 || limited != 4 {
		t.Fatalf("created %d, limited %d", success, limited)
	}
}

func TestScheduleClaims(t *testing.T) {
	url := storetest.URL(t)
	st, err := store.Open(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err = conn.Exec(t.Context(), `SET search_path TO core`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	makeRoutine := func(name string, due time.Time) store.Routine {
		t.Helper()
		r, err := st.CreateRoutine(t.Context(), store.Routine{Name: name, ScheduleKind: "cron", ScheduleExpr: "* * * * *", Timezone: "UTC", Agent: "eika", Prompt: "test", CreatedBy: "user", Enabled: true, ApprovedAt: now, NextRunAt: due})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	old := makeRoutine("missed", now.Add(-7*time.Hour))
	locked := makeRoutine("locked", now.Add(-time.Minute))
	current := makeRoutine("current", now.Add(-time.Minute))
	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(t.Context(), `SELECT id FROM routines WHERE id=$1 FOR UPDATE`, locked.ID); err != nil {
		t.Fatal(err)
	}
	run, ok, err := st.ClaimRoutine(t.Context(), now)
	if err != nil || !ok || run.ID != current.ID {
		t.Fatalf("claim: %+v %v %v", run, ok, err)
	}
	var outcome string
	if err = conn.QueryRow(t.Context(), `SELECT outcome FROM routine_runs WHERE routine_id=$1`, old.ID).Scan(&outcome); err != nil || outcome != "skipped" {
		t.Fatalf("missed: %s %v", outcome, err)
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(t.Context(), `UPDATE routines SET enabled=false WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	unlocked, ok, err := st.ClaimRoutine(t.Context(), now)
	if err != nil || !ok {
		t.Fatalf("claim unlocked: %v %v", ok, err)
	}
	if _, ok, err = st.ClaimRoutine(t.Context(), now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("overlap: %v %v", ok, err)
	}
	if err = conn.QueryRow(t.Context(), `SELECT reason FROM routine_runs WHERE routine_id=$1 AND outcome='skipped'`, current.ID).Scan(&outcome); err != nil || outcome == "" {
		t.Fatalf("overlap reason: %q %v", outcome, err)
	}
	if err = st.FinishRoutine(t.Context(), unlocked.RunID, "", "done", "", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = st.RecoverSchedule(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRow(t.Context(), `SELECT outcome FROM routine_runs WHERE id=$1`, run.RunID).Scan(&outcome); err != nil || outcome != "interrupted" {
		t.Fatalf("recovery: %s %v", outcome, err)
	}
}

func TestWakeupMessageIsStoredOnce(t *testing.T) {
	st := storetest.Open(t)
	sess, err := st.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	wake, err := st.CreateWakeup(t.Context(), schedule.Wakeup{SessionID: sess.ID, Agent: sess.Agent, Note: "check", DueAt: now})
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := st.ClaimWakeup(t.Context(), now)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	entry, err := session.MessageEntry(provider.UserMessage("<context>wakeup: check</context>"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.AppendWakeup(t.Context(), wake.ID, sess.ID, entry); err != nil {
		t.Fatal(err)
	}
	if err = st.AppendWakeup(t.Context(), wake.ID, sess.ID, entry); err != nil {
		t.Fatal(err)
	}
	path, err := st.SessionPath(t.Context(), sess.ID)
	if err != nil || len(path) != 1 {
		t.Fatalf("path length: %d, %v", len(path), err)
	}
	if err = st.RecoverSchedule(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = st.ClaimWakeup(t.Context(), now); err != nil || ok {
		t.Fatalf("claimed again: %v %v", ok, err)
	}
}
