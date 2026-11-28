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
// not anchored). The settings are read only when a chain could complete, so a
// plain fetch costs a map lookup. A chain that completes stops the call with
// a verdict the model cannot learn from; the hold that goes with it is the
// held run's (P4b-08). An error reading the setting lets the call run and is
// logged, like a mark that cannot be written.
func (g *gate) tripwire(ctx context.Context, a Action, sink fetchSink, carried bool) Verdict {
	p := a.Principal
	if p.Run == "" || sink.host == "" {
		return Verdict{Decision: Allow}
	}
	watched := g.isWatched(p.Session, sink.host)
	if !watched && (!carried || !sink.newSink) {
		return Verdict{Decision: Allow}
	}
	if watched {
		g.trip.Add(p.Run, trip.Mark{Kind: trip.Watched, Call: a.Key, Computer: p.Computer})
	}
	if sink.newSink {
		g.trip.Add(p.Run, trip.Mark{Kind: trip.NewSink, Call: a.Key, Computer: p.Computer})
	}
	s, err := g.tripwires(ctx)
	if err != nil {
		g.Log.Error("read tripwires", "error", err)
		return Verdict{Decision: Allow}
	}
	name, ok := g.trip.Check(s, p.Run, a.Key, p.Computer)
	if !ok {
		return Verdict{Decision: Allow}
	}
	return Verdict{Decision: Deny, Reason: tripReason, Detail: "tripwire " + string(name)}
}
