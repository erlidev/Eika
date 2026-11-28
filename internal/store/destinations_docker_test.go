//go:build docker

package store_test

import (
	"testing"

	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestDestinationIndexKeepsFirstSourceUserWinsAndIsInherited(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	var _ sentry.DestinationLog = (*store.Store)(nil)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.RecordDestinations(ctx, "s1", []string{"a.example", "b.example", "a.example"}, sentry.DestContent, "web:p.example"))
	must(st.RecordDestinations(ctx, "s1", []string{"a.example"}, sentry.DestSearch, "web:q.example"))
	must(st.RecordDestinations(ctx, "s1", []string{"b.example"}, sentry.DestUser, "user"))
	must(st.RecordDestinations(ctx, "s1", []string{"b.example"}, sentry.DestContent, "web:p.example"))
	must(st.InheritDestinations(ctx, "s1", "child"))
	must(st.RecordDestinations(ctx, "s1", []string{"c.example"}, sentry.DestSearch, "web:q.example"))

	for _, tc := range []struct {
		session, host string
		want          sentry.DestSource
		found         bool
	}{
		{"s1", "a.example", sentry.DestContent, true},
		{"s1", "b.example", sentry.DestUser, true},
		{"child", "b.example", sentry.DestUser, true},
		{"child", "c.example", "", false},
	} {
		got, ok, err := st.DestinationSource(ctx, tc.session, tc.host)
		if err != nil || ok != tc.found || got != tc.want {
			t.Errorf("%s %s = %q, %v, %v; want %q, %v", tc.session, tc.host, got, ok, err, tc.want, tc.found)
		}
	}

	must(st.MarkKnown(ctx, "", "fine.example", "fine"))
	must(st.MarkKnown(ctx, "c1", "reached.example", "reached"))
	for _, tc := range []struct {
		computer, host string
		want           bool
	}{
		{"c2", "fine.example", true},
		{"c1", "reached.example", true},
		{"c2", "reached.example", false},
	} {
		if got, err := st.KnownHost(ctx, tc.computer, tc.host); err != nil || got != tc.want {
			t.Errorf("known %s on %s = %v, %v; want %v", tc.host, tc.computer, got, err, tc.want)
		}
	}
}
