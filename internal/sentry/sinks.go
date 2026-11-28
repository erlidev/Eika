package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/maphash"
	"net/url"
	"strings"

	"github.com/erlidev/eika/internal/sentry/trip"
)

// The sink marks (docs/design/sentry.md, section 11). Each says where an action
// or a connection went; none changes a verdict.
const (
	// MarkNewSink: a destination is not anchored. Value is the host.
	MarkNewSink MarkKind = "new-sink"
	// MarkToldSink: a connection's host has the source content, so the agent
	// learned of it from something it read. Value is the host.
	MarkToldSink MarkKind = "told-sink"
	// MarkUpload: a connection to a host that is not anchored sent a lot.
	MarkUpload MarkKind = "upload"
	// MarkFanOut: a run fetched many different URLs on a host only content named.
	MarkFanOut MarkKind = "fan-out"
)

// SinkLimits are the numbers of the upload and fan-out marks (the
// `sink_limits` setting). Each is a whole number of at least 1.
type SinkLimits struct {
	// UploadBytes is the bytes sent to one host that is not anchored, in one
	// run, over which `upload` is marked.
	UploadBytes int64 `json:"upload_bytes"`
	// RatioBytes is the lower size at which a lopsided exchange counts: over
	// this many bytes sent and Ratio times more sent than received.
	RatioBytes int64 `json:"upload_ratio_bytes"`
	Ratio      int64 `json:"upload_ratio"`
	// FanOutURLs is the number of different URLs one run may fetch on a host
	// only content named; the next one marks `fan-out`.
	FanOutURLs int `json:"fan_out_urls"`
}

// DefaultSinkLimits is the starting value of the setting.
var DefaultSinkLimits = SinkLimits{UploadBytes: 1 << 20, RatioBytes: 64 << 10, Ratio: 10, FanOutURLs: 16}

const (
	maxSinkBytes  = 1 << 40
	maxSinkRatio  = 1_000_000
	maxFanOutURLs = 256
)

// Validate checks a value of the setting.
func (l SinkLimits) Validate() error {
	switch {
	case l.UploadBytes < 1 || l.UploadBytes > maxSinkBytes:
		return fmt.Errorf("upload_bytes must be 1 to %d", int64(maxSinkBytes))
	case l.RatioBytes < 1 || l.RatioBytes > maxSinkBytes:
		return fmt.Errorf("upload_ratio_bytes must be 1 to %d", int64(maxSinkBytes))
	case l.Ratio < 1 || l.Ratio > maxSinkRatio:
		return fmt.Errorf("upload_ratio must be 1 to %d", maxSinkRatio)
	case l.FanOutURLs < 1 || l.FanOutURLs > maxFanOutURLs:
		return fmt.Errorf("fan_out_urls must be 1 to %d", maxFanOutURLs)
	}
	return nil
}

// ParseSinkLimits reads a stored value; a field it leaves out keeps its default.
func ParseSinkLimits(raw json.RawMessage) (SinkLimits, error) {
	l := DefaultSinkLimits
	if err := json.Unmarshal(raw, &l); err != nil {
		return SinkLimits{}, err
	}
	return l, l.Validate()
}

func (g *gate) sinkLimits(ctx context.Context) (SinkLimits, error) {
	if g.SinkLimits == nil {
		return DefaultSinkLimits, nil
	}
	return g.SinkLimits(ctx)
}

// markOnce writes a sink mark the first time a run has it for a host.
func (g *gate) markOnce(ctx context.Context, m Mark) {
	if !g.firstMark(m.Run, m.Computer, string(m.Kind)+" "+m.Value) {
		return
	}
	if err := g.mark(ctx, m); err != nil {
		g.Log.Error("record sink mark", "kind", m.Kind, "error", err)
	}
}

// markFetchSink marks the destination of an allowed web_fetch: new-sink when it
// is not anchored, and fan-out when the run has fetched more different URLs
// than the limit on a host only content named. A URL that does not parse is
// not marked. The result tells the tripwires what this one call reached.
func (g *gate) markFetchSink(ctx context.Context, a Action, args []string) (sink fetchSink) {
	p := a.Principal
	if g.Marks == nil || p.Run == "" || a.Name != "web_fetch" || len(args) == 0 {
		return
	}
	u, err := url.Parse(strings.TrimSpace(args[0]))
	if err != nil {
		return
	}
	host := fetchHost(args)
	if host == "" {
		return
	}
	sink.host = host
	mk := func(kind MarkKind) Mark {
		return Mark{Session: p.Session, Run: p.Run, Computer: p.Computer, Kind: kind, Value: host, SourceName: a.Name, Key: a.Key}
	}
	anchored, err := g.anchored(ctx, p.Session, "", host, false)
	if err != nil {
		g.Log.Error("read anchored", "error", err)
		return
	}
	if !anchored {
		sink.newSink = true
		g.markOnce(ctx, mk(MarkNewSink))
	}
	if g.Destinations == nil {
		return
	}
	src, ok, err := g.Destinations.DestinationSource(ctx, p.Session, host)
	if err != nil {
		g.Log.Error("read destination source", "error", err)
		return
	}
	if !ok || src != DestContent {
		return
	}
	limits, err := g.sinkLimits(ctx)
	if err != nil {
		g.Log.Error("read sink limits", "error", err)
		return
	}
	u.Fragment = ""
	if g.fetchedURLs(p.Run, p.Computer, host, u.String(), limits.FanOutURLs) > limits.FanOutURLs {
		sink.fanOut = true
		g.trip.Add(p.Run, trip.Mark{Kind: trip.FanOut, Call: a.Key, Computer: p.Computer})
		g.markOnce(ctx, mk(MarkFanOut))
	}
	return
}

// fetchSink is what markFetchSink learned about a fetch's destination.
type fetchSink struct {
	host    string
	newSink bool // the host is not anchored, whether or not the mark was written now
	fanOut  bool // this fetch is past the run's limit of URLs on the host
}

// markConnectionSink marks an allowed connection's host when it is not
// anchored: new-sink, and told-sink too when only content named it. The run is
// the one the computer is on; without one nothing is marked. A host that is
// anchored is never told-sink: a known one was reached before and a page
// naming it is ordinary.
func (g *gate) markConnectionSink(ctx context.Context, a Action) (sink connSink) {
	p := a.Principal
	if g.Marks == nil || p.Run == "" {
		return
	}
	host := EgressHost(a.Destination)
	if host == "" {
		return
	}
	anchored, err := g.anchored(ctx, p.Session, p.Computer, host, true)
	if err != nil {
		g.Log.Error("read anchored", "error", err)
		return
	}
	if anchored {
		return
	}
	sink.host = host
	mk := func(kind MarkKind) Mark {
		return Mark{Session: p.Session, Run: p.Run, Computer: p.Computer, Kind: kind, Value: host, SourceName: "egress", Key: a.Key}
	}
	g.markOnce(ctx, mk(MarkNewSink))
	if g.Destinations == nil {
		return
	}
	src, ok, err := g.Destinations.DestinationSource(ctx, p.Session, host)
	if err != nil {
		g.Log.Error("read destination source", "error", err)
		return
	}
	if ok && src == DestContent {
		sink.told = true
		g.markOnce(ctx, mk(MarkToldSink))
	}
	return
}

// connSink is what markConnectionSink learned about a connection's host.
type connSink struct {
	host string
	told bool // only content named the host, and it is not anchored
}

// markUpload marks a host a run's computer has sent a lot to, after each
// report, when the host is not anchored.
func (g *gate) markUpload(ctx context.Context, o Observation) {
	if g.Marks == nil || o.Run == "" {
		return
	}
	host := EgressHost(o.Host)
	c := g.egressCount(o.Run, host)
	if host == "" || c.Sent == 0 || g.hasMark(o.Run, string(MarkUpload)+" "+host) {
		return
	}
	limits, err := g.sinkLimits(ctx)
	if err != nil {
		g.Log.Error("read sink limits", "error", err)
		return
	}
	lopsided := c.Sent > limits.RatioBytes && c.Sent >= limits.Ratio*c.Received
	if c.Sent <= limits.UploadBytes && !lopsided {
		return
	}
	anchored, err := g.anchored(ctx, o.Session, o.Computer, host, true)
	if err != nil {
		g.Log.Error("read anchored", "error", err)
		return
	}
	if anchored {
		return
	}
	g.markOnce(ctx, Mark{Session: o.Session, Run: o.Run, Computer: o.Computer, Kind: MarkUpload, Value: host, SourceName: "egress"})
	g.drained(ctx, Principal{Session: o.Session, Run: o.Run, Computer: o.Computer}, "upload "+host, host, trip.Upload)
}

// urlSeed hashes fetched URLs; the ledger holds hashes, not addresses.
var urlSeed = maphash.MakeSeed()
