//go:build docker

package store_test

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestThePrefillRateIsWhatTheEndpointsRecentCallsMeasured(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	sess := newSession(t, st, store.Session{})
	run, err := st.StartRun(ctx, sess.ID)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	record := func(endpoint string, prefilled, prefillMS *int) {
		t.Helper()
		if _, err := st.RecordModelCall(ctx, store.ModelCall{
			SessionID: sess.ID, RunID: run.ID, Model: "m", Endpoint: endpoint,
			PrefilledTokens: prefilled, PrefillMS: prefillMS,
		}); err != nil {
			t.Fatalf("RecordModelCall: %v", err)
		}
	}

	if _, ok, err := st.PrefillRate(ctx, "local"); err != nil || ok {
		t.Fatalf("PrefillRate before any call = ok %v, %v; want no measurement", ok, err)
	}

	record("local", new(1000), new(500))
	record("local", new(3000), new(1000))
	// A short prompt is mostly the call's fixed cost, a call that stated no
	// time or no tokens says nothing, and another endpoint is another server.
	record("local", new(100), new(10))
	record("local", nil, new(900))
	record("local", new(2000), nil)
	record("other", new(2000), new(10000))
	got, ok, err := st.PrefillRate(ctx, "local")
	if err != nil || !ok || got != 2666 {
		t.Errorf("PrefillRate = %d, %v, %v; want 2666 (4000 tokens in 1.5 s), measured", got, ok, err)
	}
	if got, ok, err := st.PrefillRate(ctx, "other"); err != nil || !ok || got != 200 {
		t.Errorf("PrefillRate of another endpoint = %d, %v, %v; want its own 200", got, ok, err)
	}

	// Only the newest calls count, so a server that changed is followed.
	record("fast", new(1000), new(10000))
	for range 20 {
		record("fast", new(1000), new(100))
	}
	if got, ok, err := st.PrefillRate(ctx, "fast"); err != nil || !ok || got != 10000 {
		t.Errorf("PrefillRate = %d, %v, %v; want 10000 from the 20 newest calls, without the slow one before them", got, ok, err)
	}
}
