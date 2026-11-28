package sentry

import (
	"context"
	"hash/maphash"
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
	marked   map[string]bool                // sink marks written, by kind and host
	urls     map[string]map[uint64]struct{} // hashes of the URLs fetched, by host
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
	run := g.ledger(o.Run, o.Computer)
	if run == nil {
		return
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

// ledger returns a run's ledger, made on first use, or nil past the bound.
// The caller holds egressMu.
func (g *gate) ledger(runID, computer string) *runEgress {
	run := g.egress[runID]
	if run == nil {
		if len(g.egress) >= maxLedgerRuns {
			return nil
		}
		if g.egress == nil {
			g.egress = map[string]*runEgress{}
		}
		run = &runEgress{computer: computer, hosts: map[string]*EgressCount{}, marked: map[string]bool{}, urls: map[string]map[uint64]struct{}{}}
		g.egress[runID] = run
	}
	return run
}

// firstMark reports whether the run has not yet written the sink mark named by
// key, and notes it. Past the bound it reports false: a mark may be lost, and
// a mark alone changes nothing.
func (g *gate) firstMark(runID, computer, key string) bool {
	g.egressMu.Lock()
	defer g.egressMu.Unlock()
	run := g.ledger(runID, computer)
	if run == nil || run.marked[key] || len(run.marked) >= 3*maxLedgerHosts {
		return false
	}
	run.marked[key] = true
	return true
}

// hasMark reports whether firstMark has already taken the key.
func (g *gate) hasMark(runID, key string) bool {
	g.egressMu.Lock()
	defer g.egressMu.Unlock()
	run := g.egress[runID]
	return run != nil && run.marked[key]
}

// fetchedURLs notes a URL a run fetched on a host and returns how many
// different ones it has, counting no further than limit+1.
func (g *gate) fetchedURLs(runID, computer, host, rawURL string, limit int) int {
	g.egressMu.Lock()
	defer g.egressMu.Unlock()
	run := g.ledger(runID, computer)
	if run == nil {
		return 0
	}
	set := run.urls[host]
	if set == nil {
		if len(run.urls) >= 64 {
			return 0
		}
		set = map[uint64]struct{}{}
		run.urls[host] = set
	}
	if len(set) <= limit {
		set[maphash.String(urlSeed, rawURL)] = struct{}{}
	}
	return len(set)
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
