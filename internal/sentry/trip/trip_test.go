package trip_test

import (
	"testing"

	"github.com/erlidev/eika/internal/sentry/trip"
)

var allOn = trip.Settings{Followed: true, CarriedOut: true, Lifted: true, Drained: true, BlockedOften: true, BlockedLimit: 10}

func TestChains(t *testing.T) {
	m := func(k trip.Kind, call, computer string) trip.Mark {
		return trip.Mark{Kind: k, Call: call, Computer: computer}
	}
	tests := []struct {
		name  string
		marks []trip.Mark
		set   trip.Settings
		want  trip.Tripwire // empty: nothing fires
	}{
		{"followed", []trip.Mark{m(trip.Watched, "c", "x")}, allOn, trip.Followed},
		{"followed off", []trip.Mark{m(trip.Watched, "c", "x")}, trip.Settings{CarriedOut: true}, ""},
		{"watched on another call", []trip.Mark{m(trip.Watched, "old", "x")}, allOn, ""},
		{"carried out", []trip.Mark{m(trip.Carried, "c", "x"), m(trip.NewSink, "c", "x")}, allOn, trip.CarriedOut},
		{"carried and sink on different calls", []trip.Mark{m(trip.Carried, "old", "x"), m(trip.NewSink, "c", "x")}, allOn, ""},
		{"lifted", []trip.Mark{m(trip.SensitiveRead, "r", "x"), m(trip.ToldSink, "c", "x")}, allOn, trip.Lifted},
		{"lifted needs the read first", []trip.Mark{m(trip.ToldSink, "c", "x"), m(trip.SensitiveRead, "r", "x")}, allOn, ""},
		{"lifted needs the same computer", []trip.Mark{m(trip.SensitiveRead, "r", "y"), m(trip.ToldSink, "c", "x")}, allOn, ""},
		{"lifted needs told-sink, not new-sink", []trip.Mark{m(trip.SensitiveRead, "r", "x"), m(trip.NewSink, "c", "x")}, allOn, ""},
		{"drained", []trip.Mark{m(trip.Upload, "c", "x")}, allOn, trip.Drained},
		{"drained alone only", []trip.Mark{m(trip.Carried, "r", "x"), m(trip.FanOut, "c", "x")}, allOn, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var e trip.Engine
			for _, mk := range tt.marks {
				e.Add("run", mk)
			}
			got, ok := e.Check(tt.set, "run", "c", "x")
			if got != tt.want || ok != (tt.want != "") {
				t.Fatalf("Check = %q, %v; want %q", got, ok, tt.want)
			}
			if _, ok := e.Check(tt.set, "other run", "c", "x"); ok {
				t.Fatal("another run's marks joined this one")
			}
		})
	}
}

func TestParseSettings(t *testing.T) {
	s, err := trip.Parse([]byte(`{"followed":true}`))
	if err != nil || !s.Followed || s.CarriedOut || s.BlockedLimit != trip.DefaultBlockedLimit {
		t.Fatalf("Parse = %+v, %v", s, err)
	}
	if _, err := trip.Parse([]byte(`{"blocked_limit":0}`)); err == nil {
		t.Fatal("a limit of 0 was accepted")
	}
}

func TestBlockedCountsPerRun(t *testing.T) {
	var e trip.Engine
	for range 3 {
		e.Blocked("a")
	}
	if got := e.Blocked("b"); got != 1 {
		t.Fatalf("run b counted %d", got)
	}
	if got := e.Blocked("a"); got != 4 {
		t.Fatalf("run a counted %d", got)
	}
}
