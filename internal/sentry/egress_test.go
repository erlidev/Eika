package sentry_test

import (
	"context"
	"testing"

	"github.com/erlidev/eika/internal/sentry"
)

func TestEgressDenialsAreAudited(t *testing.T) {
	m := &memHalt{}
	g := sentry.New(sentry.Options{Audit: m, Halts: m, BlockedHost: func(_ context.Context, host string) (bool, error) {
		return host == "blocked.example" || host == "192.0.2.1", nil
	}})
	cases := []struct {
		host, reason string
		want         sentry.Decision
	}{
		{"allowed.example", "", sentry.Allow},
		{"BLOCKED.example.", "", sentry.Deny},
		{"::ffff:192.0.2.1", "", sentry.Deny},
		{"allowed.example", "not on allowlist", sentry.Deny},
		{"allowed.example", "non-public address", sentry.Deny},
	}
	for _, c := range cases {
		v, err := g.Decide(t.Context(), sentry.Action{Kind: sentry.KindEgress, Principal: sentry.Principal{Computer: "cx"}, Name: c.host, Destination: c.host, EgressDenial: c.reason})
		if err != nil || v.Decision != c.want {
			t.Fatalf("%+v: %+v %v", c, v, err)
		}
	}
	if len(m.audit) != 4 {
		t.Fatalf("audit rows: %d", len(m.audit))
	}
	for _, row := range m.audit {
		if row.Event != sentry.EventDeny || row.Kind != "egress" || row.Principal.Computer != "cx" {
			t.Fatalf("audit row: %+v", row)
		}
	}
}
