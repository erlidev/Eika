package sentry_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/sentry"
)

func sinkGate(marks *markRecords, dest *destLog, limits *sentry.SinkLimits) sentry.Gate {
	o := sentry.Options{Audit: &records{}, Marks: marks, Destinations: dest}
	if limits != nil {
		o.SinkLimits = func(context.Context) (sentry.SinkLimits, error) { return *limits, nil }
	}
	return sentry.New(o)
}

func fetch(t *testing.T, g sentry.Gate, url string) {
	t.Helper()
	a := sentry.Action{Key: "s:c", Principal: sentry.Principal{Session: "s", Run: "r", Computer: "cx"}, Kind: sentry.KindTool,
		Name: "web_fetch", Args: []byte(fmt.Sprintf(`{"url":%q}`, url))}
	if v, err := g.Decide(t.Context(), a); err != nil || v.Decision != sentry.Allow {
		t.Fatalf("fetch %s: %+v, %v; a mark must change no verdict", url, v, err)
	}
}

func TestFetchMarksNewSinkUnlessAnchored(t *testing.T) {
	dest := newDestLog()
	marks := &markRecords{}
	g := sinkGate(marks, dest, nil)
	_ = dest.RecordDestinations(t.Context(), "s", []string{"typed.example"}, sentry.DestUser, "")
	_ = dest.RecordDestinations(t.Context(), "s", []string{"found.example"}, sentry.DestSearch, "")
	_ = dest.RecordDestinations(t.Context(), "s", []string{"page.example"}, sentry.DestContent, "")
	for _, u := range []string{"https://typed.example/a", "https://found.example/a", "https://page.example/a", "https://page.example/b", "https://made-up.example/"} {
		fetch(t, g, u)
	}
	if got := marks.values(sentry.MarkNewSink); !slices.Equal(got, []string{"page.example", "made-up.example"}) {
		t.Errorf("new-sink hosts = %v, want content and model hosts once each", got)
	}
}

func TestFanOutNeedsMoreThanTheLimitOnAContentHost(t *testing.T) {
	dest := newDestLog()
	marks := &markRecords{}
	g := sinkGate(marks, dest, &sentry.SinkLimits{UploadBytes: 1 << 20, RatioBytes: 1 << 16, Ratio: 10, FanOutURLs: 3})
	_ = dest.RecordDestinations(t.Context(), "s", []string{"page.example"}, sentry.DestContent, "")
	_ = dest.RecordDestinations(t.Context(), "s", []string{"found.example"}, sentry.DestSearch, "")
	for i := range 6 {
		fetch(t, g, "https://found.example/"+fmt.Sprint(i))
		fetch(t, g, "https://page.example/same#"+fmt.Sprint(i%2))
		fetch(t, g, "https://page.example/"+fmt.Sprint(i%2))
	}
	if got := marks.values(sentry.MarkFanOut); len(got) != 0 {
		t.Fatalf("fan-out = %v with three different URLs and a limit of three", got)
	}
	fetch(t, g, "https://page.example/fourth")
	fetch(t, g, "https://page.example/fifth")
	if got := marks.values(sentry.MarkFanOut); !slices.Equal(got, []string{"page.example"}) {
		t.Errorf("fan-out = %v, want page.example once", got)
	}
}

func TestConnectionsMarkToldSinkAndUpload(t *testing.T) {
	ctx := context.Background()
	dest := newDestLog()
	marks := &markRecords{}
	g := sinkGate(marks, dest, nil)
	_ = dest.RecordDestinations(ctx, "s", []string{"told.example", "github.com"}, sentry.DestContent, "")
	_ = dest.RecordDestinations(ctx, "s", []string{"typed.example"}, sentry.DestUser, "")
	connect := func(host string) {
		a := sentry.Action{Key: "k", Principal: sentry.Principal{Session: "s", Run: "r", Computer: "cx"}, Kind: sentry.KindEgress, Name: host, Destination: host}
		if v, err := g.Decide(ctx, a); err != nil || v.Decision != sentry.Allow {
			t.Fatalf("connect %s: %+v, %v", host, v, err)
		}
	}
	for _, h := range []string{"told.example", "github.com", "typed.example", "model.example", "told.example"} {
		connect(h)
	}
	if got := marks.values(sentry.MarkToldSink); !slices.Equal(got, []string{"told.example"}) {
		t.Errorf("told-sink = %v, want only the content host that is not anchored, once", got)
	}
	if got := marks.values(sentry.MarkNewSink); !slices.Equal(got, []string{"told.example", "model.example"}) {
		t.Errorf("new-sink = %v", got)
	}

	send := func(host string, sent, got int64) {
		g.Observe(ctx, sentry.Observation{Kind: sentry.ObserveEgress, Session: "s", Run: "r", Computer: "cx", Host: host, Sent: sent, Received: got})
	}
	send("model.example", 600<<10, 0)
	send("model.example", 600<<10, 0)   // over 1 MiB in all
	send("lopsided.example", 70<<10, 1) // over 64 KiB, far more sent than received
	send("balanced.example", 200<<10, 100<<10)
	send("typed.example", 5<<20, 0) // anchored
	send("github.com", 5<<20, 0)    // shipped
	if got := marks.values(sentry.MarkUpload); !slices.Equal(got, []string{"model.example", "lopsided.example"}) {
		t.Errorf("upload = %v, want the two hosts that are not anchored, once each", got)
	}
}
