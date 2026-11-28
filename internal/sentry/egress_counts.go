package sentry

import (
	"context"
	"sort"
)

// Bounds on the egress ledger, which lives in memory for the length of a run.
const (
	maxLedgerRuns  = 256
	maxLedgerHosts = 512
)

// EgressCount is what one run's computer moved to one host.
type EgressCount struct {
	Connections    int
	Sent, Received int64
}

// runEgress is the ledger of one run.
type runEgress struct {
	computer string
	hosts    map[string]*EgressCount
}

// countEgress adds one proxy report to its run's ledger. A report without a
// run, or past the bounds, is dropped: the counts only ever make a mark more
// likely, so losing some never allows anything.
func (g *gate) countEgress(o Observation) {
	host := EgressHost(o.Host)
	if o.Run == "" || host == "" {
		return
	}
	g.egressMu.Lock()
	defer g.egressMu.Unlock()
	run := g.egress[o.Run]
	if run == nil {
		if len(g.egress) >= maxLedgerRuns {
			return
		}
		if g.egress == nil {
			g.egress = map[string]*runEgress{}
		}
		run = &runEgress{computer: o.Computer, hosts: map[string]*EgressCount{}}
		g.egress[o.Run] = run
	}
	c := run.hosts[host]
	if c == nil {
		if len(run.hosts) >= maxLedgerHosts {
			return
		}
		c = &EgressCount{}
		run.hosts[host] = c
	}
	c.Sent += o.Sent
	c.Received += o.Received
	if o.Closed {
		c.Connections++
	}
}

// egressCount returns what a run's computer has moved to a host so far.
func (g *gate) egressCount(run, host string) EgressCount {
	g.egressMu.Lock()
	defer g.egressMu.Unlock()
	if r := g.egress[run]; r != nil {
		if c := r.hosts[EgressHost(host)]; c != nil {
			return *c
		}
	}
	return EgressCount{}
}

// endRun drops a finished run's ledger. A host its computer exchanged bytes
// with becomes known to that computer, so a later run reaching it is not new.
func (g *gate) endRun(ctx context.Context, run string) {
	g.egressMu.Lock()
	r := g.egress[run]
	delete(g.egress, run)
	g.egressMu.Unlock()
	if r == nil || g.Destinations == nil || r.computer == "" {
		return
	}
	var reached []string
	for host, c := range r.hosts {
		if c.Sent+c.Received > 0 {
			reached = append(reached, host)
		}
	}
	sort.Strings(reached)
	for _, host := range reached {
		if err := g.Destinations.MarkKnown(ctx, r.computer, host, "reached"); err != nil {
			g.Log.Error("mark reached host known", "computer_id", r.computer, "host", host, "error", err)
		}
	}
}
