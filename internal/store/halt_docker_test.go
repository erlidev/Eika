//go:build docker

package store_test

import (
	"testing"
	"time"

	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/jackc/pgx/v5"
)

func TestHaltSurvivesReopenAndIsAudited(t *testing.T) {
	ctx := t.Context()
	url := storetest.URL(t)
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.Halt(ctx); err != nil || got.Halted {
		t.Fatalf("fresh database: %+v %v", got, err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	err = st.SetHalt(ctx, sentry.HaltState{Halted: true, At: at, Reason: "why"}, sentry.AuditEntry{Event: sentry.EventHalt, Reason: "why"})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	st, err = store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if got, err := st.Halt(ctx); err != nil || !got.Halted || got.Reason != "why" || !got.At.Equal(at) {
		t.Fatalf("after reopen: %+v %v", got, err)
	}
	if err := st.SetHalt(ctx, sentry.HaltState{}, sentry.AuditEntry{Event: sentry.EventResume}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Halt(ctx); got.Halted {
		t.Error("still halted after resume")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var halts, resumes int
	err = conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE event = 'halt'), count(*) FILTER (WHERE event = 'resume') FROM core.audit`).Scan(&halts, &resumes)
	if err != nil || halts != 1 || resumes != 1 {
		t.Errorf("audit rows halt=%d resume=%d, %v; want 1 each", halts, resumes, err)
	}
}
