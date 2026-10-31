package inference

import "sync/atomic"

// Priority says how urgent a model call is. A smaller value is more urgent.
type Priority int

// The priorities, most urgent first.
const (
	// Interactive is a call a person is waiting on: the answer to a message
	// the user just sent.
	Interactive Priority = iota
	// Foreground is a subagent the user started or messaged lately, or one
	// they are watching.
	Foreground
	// Background is routine work: wakeups, routines, other subagents.
	Background
	// Maintenance is work nobody waits for: titles, idle compaction,
	// dreaming. It runs only while the pool is idle.
	Maintenance
)

// priorityNames are the wire names of the priorities, in order.
var priorityNames = [...]string{"interactive", "foreground", "background", "maintenance"}

// String returns the priority's wire name.
func (p Priority) String() string {
	if p < Interactive || p > Maintenance {
		return "unknown"
	}
	return priorityNames[p]
}

// ParsePriority returns the priority a wire name stands for.
func ParsePriority(name string) (Priority, bool) {
	for i, n := range priorityNames {
		if n == name {
			return Priority(i), true
		}
	}
	return 0, false
}

// Rank is the priority of a run, shared between the code that drives the run
// and the code that learns it has become more urgent. The run reads it at
// every Acquire, so raising it changes the run's next lease and nothing
// already queued or running: a user message that joins a background run
// through its steering queue calls Raise, and the next model call goes in as
// interactive.
type Rank struct {
	p atomic.Int32
}

// NewRank returns a Rank that starts at p.
func NewRank(p Priority) *Rank {
	r := &Rank{}
	r.p.Store(int32(p))
	return r
}

// Priority returns the current priority. A nil Rank is interactive: a call
// nobody ranked is one a person started.
func (r *Rank) Priority() Priority {
	if r == nil {
		return Interactive
	}
	return Priority(r.p.Load())
}

// Raise makes the rank at least as urgent as p and reports whether it
// changed. It never lowers the rank.
func (r *Rank) Raise(p Priority) bool {
	if r == nil {
		return false
	}
	for {
		cur := r.p.Load()
		if int32(p) >= cur {
			return false
		}
		if r.p.CompareAndSwap(cur, int32(p)) {
			return true
		}
	}
}
