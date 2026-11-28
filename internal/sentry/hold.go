package sentry

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// A held run (docs/design/sentry.md, section 11) is a run whose own actions
// completed Followed, Carried out or Lifted. It carries on, but its outbound
// actions to destinations that are not anchored are blocked, and the subagents
// it starts are held. The hold lives in memory and ends with the run: a run
// does not survive a restart, so neither does its hold.

// Bounds on the hold state, like the other in-memory ledgers.
const maxHolds = 256

// Tripped is a tripwire that fired. Core turns it into the Inbox entry
// (P4b-09); the gate gives one per hold, and one per run for the tripwires that
// do not hold.
type Tripped struct {
	// Tripwire is the chain: followed, carried_out, lifted, drained or
	// blocked_often.
	Tripwire  string
	Principal Principal
	// Key is the call or connection that completed the chain.
	Key string
	// Host is the destination it reached, when it has one.
	Host string
	// Held reports whether the run is now held.
	Held bool
}

// holds is the hold state of the runs in progress.
type holds struct {
	mu       sync.Mutex
	runs     map[string]string // held run to its session
	inherit  map[string]string // subagent session to the held run that started it
	tainted  map[string]bool   // runs that acted while held, held or inherited
	notified map[string]map[string]bool
}

// held reports whether the principal's run is held: the run itself, or a
// subagent of a held run.
func (g *gate) held(p Principal) bool {
	h := &g.holds
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.runs[p.Run]; ok && p.Run != "" {
		h.tainted[p.Run] = true
		return true
	}
	if parent, ok := h.inherit[p.Session]; ok {
		if _, ok := h.runs[parent]; ok {
			if p.Run != "" && len(h.tainted) < maxHolds {
				h.tainted[p.Run] = true
			}
			return true
		}
	}
	return false
}

// startHold holds a run and reports whether it was not held before. Past the
// bound nothing is held, which only loosens: a chain still denies its call.
func (g *gate) startHold(run, session string) bool {
	h := &g.holds
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.runs[run]; ok {
		return false
	}
	if len(h.runs) >= maxHolds {
		return false
	}
	if h.runs == nil {
		h.runs, h.inherit, h.tainted = map[string]string{}, map[string]string{}, map[string]bool{}
	}
	h.runs[run] = session
	h.tainted[run] = true
	return true
}

// holdSubagent holds a new subagent session when its parent is held.
func (g *gate) holdSubagent(parent, child string) {
	h := &g.holds
	h.mu.Lock()
	defer h.mu.Unlock()
	run := ""
	for r, s := range h.runs {
		if s == parent {
			run = r
		}
	}
	if run == "" {
		run = h.inherit[parent] // a grandchild
	}
	if run != "" && len(h.inherit) < maxHolds {
		h.inherit[child] = run
	}
}

// endHold forgets a run's hold and what was said of it, and reports whether the
// run acted while held, so the hosts it reached do not become known.
func (g *gate) endHold(run string) (wasHeld bool) {
	h := &g.holds
	h.mu.Lock()
	defer h.mu.Unlock()
	wasHeld = h.tainted[run]
	delete(h.runs, run)
	delete(h.tainted, run)
	delete(h.notified, run)
	for child, parent := range h.inherit {
		if parent == run {
			delete(h.inherit, child)
		}
	}
	return wasHeld
}

// firstNotice reports whether a run has not yet been told of a tripwire, and notes it.
func (g *gate) firstNotice(run, name string) bool {
	h := &g.holds
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.notified[run][name] {
		return false
	}
	if h.notified == nil {
		h.notified = map[string]map[string]bool{}
	}
	if h.notified[run] == nil {
		if len(h.notified) >= maxHolds {
			return false
		}
		h.notified[run] = map[string]bool{}
	}
	h.notified[run][name] = true
	return true
}

// fired reports a tripwire to Core, once per run for each of them, and a hold
// once when a chain starts it.
func (g *gate) fired(ctx context.Context, name string, p Principal, key, host string, holds bool) {
	if holds {
		if !g.startHold(p.Run, p.Session) {
			return
		}
	} else if !g.firstNotice(p.Run, name) {
		return
	}
	if g.Tripped != nil {
		g.Tripped(ctx, Tripped{Tripwire: name, Principal: p, Key: key, Host: host, Held: holds})
	}
}

// fetchHost is the host a web_fetch's URL reaches, or "".
func fetchHost(args []string) string {
	if len(args) == 0 {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(args[0]))
	if err != nil {
		return ""
	}
	return EgressHost(u.Hostname())
}

// heldFetch blocks a held run's web_fetch to a destination that is not
// anchored. Everything else a held run does carries on.
func (g *gate) heldFetch(ctx context.Context, a Action, args []string) (Verdict, error) {
	if a.Name != "web_fetch" || !g.held(a.Principal) {
		return Verdict{Decision: Allow}, nil
	}
	if host := fetchHost(args); host != "" {
		ok, err := g.anchored(ctx, a.Principal.Session, "", host, false)
		if err != nil || ok {
			return Verdict{Decision: Allow}, err
		}
	}
	return Verdict{Decision: Deny, Reason: tripReason, Detail: "held run"}, nil
}

// heldConnection blocks a held run's connection to a host that is not anchored.
func (g *gate) heldConnection(ctx context.Context, a Action) (Verdict, error) {
	if !g.held(a.Principal) {
		return Verdict{Decision: Allow}, nil
	}
	p := a.Principal
	ok, err := g.anchored(ctx, p.Session, p.Computer, EgressHost(a.Destination), true)
	if err != nil || ok {
		return Verdict{Decision: Allow}, err
	}
	return Verdict{Decision: Deny, Reason: tripReason, Detail: "held run"}, nil
}

// ReleaseHold ends a run's hold and makes the destination known (the answer
// "this was fine"). The run stays marked as having acted while held, so the
// hosts it reached do not become known at its end: only the answered host does.
func (g *gate) ReleaseHold(ctx context.Context, p Principal, host string) error {
	h := &g.holds
	h.mu.Lock()
	delete(h.runs, p.Run)
	for child, parent := range h.inherit {
		if parent == p.Run {
			delete(h.inherit, child)
		}
	}
	h.mu.Unlock()
	host = EgressHost(host)
	if host == "" || g.Destinations == nil {
		return nil
	}
	if err := g.Destinations.MarkKnown(ctx, "", host, "this was fine"); err != nil {
		return fmt.Errorf("sentry: mark answered host known: %w", err)
	}
	return nil
}
