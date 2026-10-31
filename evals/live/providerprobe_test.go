package live_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
)

func TestProviderProbePassesOnTheRecordedBench(t *testing.T) {
	srv := benchServer(t)
	res := live.ProviderProbe().Execute(context.Background(), connect(t, srv.URL))
	if !res.Passed {
		t.Fatalf("provider_probe failed on the recorded bench: %s", res.Error)
	}
	if len(res.Calls) != 0 {
		t.Errorf("the scenario made %d model calls, want none", len(res.Calls))
	}
}

// otherSlots serves the bench with its /slots answer replaced.
func otherSlots(t *testing.T, slots string) *httptest.Server {
	t.Helper()
	bench := benchServer(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" {
			_, _ = w.Write([]byte(slots))
			return
		}
		bench.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProviderProbeFailsWhenSlotsDisagreeWithTheProbe(t *testing.T) {
	tests := []struct {
		name, slots, want string
	}{
		{"a missing slot", `[{"id":0,"n_ctx":131072}]`, "lists 1"},
		{"a slot with another context", `[{"id":0,"n_ctx":131072},{"id":1,"n_ctx":65536}]`, "slot 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := otherSlots(t, tt.slots)
			res := live.ProviderProbe().Execute(context.Background(), connect(t, srv.URL))
			if res.Passed || !strings.Contains(res.Error, tt.want) {
				t.Errorf("passed = %v, error = %q, want a failure mentioning %q", res.Passed, res.Error, tt.want)
			}
		})
	}
}
