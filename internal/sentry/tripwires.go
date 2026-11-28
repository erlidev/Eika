package sentry

import (
	"context"

	"github.com/erlidev/eika/internal/sentry/trip"
)

// Bounds on the watched set, which lives in memory.
const (
	maxWatchedSessions = 64
	maxWatchedHosts    = 2048
)

// tripReason is what the model reads when a tripwire stops its call. It says
// nothing of why.
const tripReason = "this is not available right now"

// noteWatched remembers a host a session's ingest watched. A host past the
// bounds is lost, which can only keep a tripwire from firing.
func (g *gate) noteWatched(session, host string) {
	if session == "" || host == "" {
		return
	}
	g.watchedMu.Lock()
	defer g.watchedMu.Unlock()
	set := g.watchedHosts[session]
	if set == nil {
		if len(g.watchedHosts) >= maxWatchedSessions {
			return
		}
		if g.watchedHosts == nil {
			g.watchedHosts = map[string]map[string]struct{}{}
		}
		set = map[string]struct{}{}
		g.watchedHosts[session] = set
	}
	if len(set) < maxWatchedHosts {
		set[host] = struct{}{}
	}
}

func (g *gate) isWatched(session, host string) bool {
	g.watchedMu.Lock()
	defer g.watchedMu.Unlock()
	_, ok := g.watchedHosts[session][host]
	return ok
}

// tripwires reads the settings: every tripwire is off until the setting says
// otherwise.
func (g *gate) tripwires(ctx context.Context) (trip.Settings, error) {
	if g.Tripwires == nil {
		return trip.Settings{}, nil
	}
	return g.Tripwires(ctx)
}

// tripwire asks whether an allowed web_fetch completes a chain: Followed (its
// host is watched) or Carried out (it carries private text to a host that is
// not anchored), which stop the call and hold the run, or Drained (it is past
// the fan-out limit with no other mark), which only tells Core. The settings
// are read only when a chain could complete, so a plain fetch costs a map
// lookup. A chain that completes stops the call with a verdict the model
// cannot learn from. An error reading the setting lets the call run and is
// logged, like a mark that cannot be written.
func (g *gate) tripwire(ctx context.Context, a Action, sink fetchSink, carried bool) Verdict {
	p := a.Principal
	if p.Run == "" || sink.host == "" {
		return Verdict{Decision: Allow}
	}
	watched := g.isWatched(p.Session, sink.host) && !g.answeredFine(ctx, sink.host)
	if !watched && !sink.fanOut && (!carried || !sink.newSink) {
		return Verdict{Decision: Allow}
	}
	if watched {
		g.trip.Add(p.Run, trip.Mark{Kind: trip.Watched, Call: a.Key, Computer: p.Computer})
	}
	if sink.newSink {
		g.trip.Add(p.Run, trip.Mark{Kind: trip.NewSink, Call: a.Key, Computer: p.Computer})
	}
	return g.check(ctx, p, a.Key, sink.host)
}

// connectionTripwire asks whether an allowed connection completes Lifted: it
// reaches a host only content named, after the run read something sensitive on
// the same computer. The connection is refused and the run held.
func (g *gate) connectionTripwire(ctx context.Context, a Action, sink connSink) Verdict {
	p := a.Principal
	if !sink.told || p.Run == "" {
		return Verdict{Decision: Allow}
	}
	g.trip.Add(p.Run, trip.Mark{Kind: trip.NewSink, Call: a.Key, Computer: p.Computer})
	g.trip.Add(p.Run, trip.Mark{Kind: trip.ToldSink, Call: a.Key, Computer: p.Computer})
	return g.check(ctx, p, a.Key, sink.host)
}

// check asks the engine which tripwire the call completes and acts on it: a
// holding one denies the call and holds the run; Drained only reports.
func (g *gate) check(ctx context.Context, p Principal, key, host string) Verdict {
	s, err := g.tripwires(ctx)
	if err != nil {
		g.Log.Error("read tripwires", "error", err)
		return Verdict{Decision: Allow}
	}
	name, ok := g.trip.Check(s, p.Run, key, p.Computer)
	if !ok {
		return Verdict{Decision: Allow}
	}
	if name == trip.Drained {
		g.fired(ctx, string(name), p, key, host, false)
		return Verdict{Decision: Allow}
	}
	g.fired(ctx, string(name), p, key, host, true)
	return Verdict{Decision: Deny, Reason: tripReason, Detail: "tripwire " + string(name)}
}

// drained asks after the fact whether an upload completes Drained: the run
// sent a lot to a host that is not anchored and has no other mark. It tells
// Core and holds nothing.
func (g *gate) drained(ctx context.Context, p Principal, key, host string, kind trip.Kind) {
	g.trip.Add(p.Run, trip.Mark{Kind: kind, Call: key, Computer: p.Computer})
	g.check(ctx, p, key, host)
}

// blockedOften counts a blocked result of a run and tells Core when the count
// reaches the setting's limit. It holds nothing.
func (g *gate) blockedOften(ctx context.Context, p Principal, key string) {
	if p.Run == "" {
		return
	}
	n := g.trip.Blocked(p.Run)
	s, err := g.tripwires(ctx)
	if err != nil {
		g.Log.Error("read tripwires", "error", err)
		return
	}
	if s.On(trip.BlockedOften) && n >= s.BlockedLimit {
		g.fired(ctx, string(trip.BlockedOften), p, key, "", false)
	}
}

// answeredFine reports whether the user answered "this was fine" for a host
// (it is known to every computer), so Followed does not fire for it again. A
// failed read says no: the tripwire fires.
func (g *gate) answeredFine(ctx context.Context, host string) bool {
	if g.Destinations == nil {
		return false
	}
	known, err := g.Destinations.KnownHost(ctx, "", host)
	return err == nil && known
}
