package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

// The defaults of Limits. They are values the options can change, because the
// agent definitions of phase 2 will: the home session keeps the call limit the
// design gives it and a subagent gets 200.
const (
	// DefaultMaxCalls is how many model calls one run may make: the home
	// session's limit in docs/design/runtime.md section 5, and the only one
	// until agents have definitions to set their own.
	DefaultMaxCalls = 40
	// DefaultMaxWall is how long one run may last, 24 hours.
	DefaultMaxWall = 24 * time.Hour
)

// repeatWarnAt is the identical consecutive tool call that gets a warning in
// its result: the third. A model that repeats a call once may have a reason.
const repeatWarnAt = 3

// The reasons a run stops at one of its limits. They are the stop_reason of
// the turn.end event the run ends with.
const (
	// StopMaxCalls means the run made as many model calls as Limits.MaxCalls
	// allows.
	StopMaxCalls = "max_calls"
	// StopMaxWall means the run lasted as long as Limits.MaxWall allows.
	StopMaxWall = "max_wall_time"
)

// Limits bound one run, so that a model stuck in a loop costs the user a
// bounded amount and says so. A zero field takes its default.
//
// Two of the guards never stop a run. A call repeated identically gets a
// warning in its result from the third (repeatWarnAt), and a stalled stream
// aborts one attempt, which the loop retries (stall.go).
type Limits struct {
	// MaxCalls is the most model calls a run makes. The calls of a retry, and
	// the summary a compaction asks for, are not counted: they are not the
	// model's own steps.
	MaxCalls int
	// MaxWall is the longest a run lasts, measured with Options.Clock and
	// checked before each model call.
	MaxWall time.Duration
	// Stall is how long a model call's stream may send nothing before the
	// attempt is aborted and tried again, once it has produced a token. Before
	// its first token the call is allowed more, in proportion to the prompt it
	// has to read (prefillAllowance). The time a call waits for a lease does not
	// count.
	Stall time.Duration
}

// withDefaults returns l with a default in every zero field.
func (l Limits) withDefaults() Limits {
	if l.MaxCalls <= 0 {
		l.MaxCalls = DefaultMaxCalls
	}
	if l.MaxWall <= 0 {
		l.MaxWall = DefaultMaxWall
	}
	if l.Stall <= 0 {
		l.Stall = DefaultStall
	}
	return l
}

// errRunLimit is what a turn returns when its run reached a limit. The turn
// has ended itself, with its event and a clean conversation, so the run ends
// without an error.
var errRunLimit = errors.New("run reached a limit")

// guard follows one run against its limits.
type guard struct {
	started time.Time
	// calls is how many model calls the run has started.
	calls int
	// last is the tool call that ran most recently, and streak how many calls
	// in a row have been identical to it.
	last   string
	streak int
}

// newGuard returns the guard of a run that begins at the time given.
func newGuard(now time.Time) *guard { return &guard{started: now} }

// stop returns the reason the run may not make another model call, or "". A run
// that has made none is never stopped: the message it began with is not stored
// until the first call has read it, and stopping before it would lose it.
func (g *guard) stop(now time.Time, l Limits) string {
	switch {
	case g.calls == 0:
		return ""
	case g.calls >= l.MaxCalls:
		return StopMaxCalls
	case now.Sub(g.started) >= l.MaxWall:
		return StopMaxWall
	}
	return ""
}

// repeated records a tool call and returns how many identical calls in a row
// the run has now made, this one included.
func (g *guard) repeated(c provider.ToolCall) int {
	key := callKey(c)
	if key == g.last {
		g.streak++
	} else {
		g.last, g.streak = key, 1
	}
	return g.streak
}

// callKey identifies a tool call by its tool and its arguments. Arguments that
// are the same JSON are the same arguments, whatever the whitespace and the
// order of the keys; arguments that are not JSON are compared as they are.
func callKey(c provider.ToolCall) string {
	return c.Name + "\x00" + canonicalArguments(c.Arguments)
}

// canonicalArguments rewrites valid JSON arguments in a form that does not
// depend on spacing or key order, and returns anything else unchanged.
func canonicalArguments(a provider.ToolArguments) string {
	dec := json.NewDecoder(bytes.NewReader([]byte(a)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return string(a)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(a)
	}
	return string(out)
}

// repeatWarning is the text added to the result of a call the run has made n
// times in a row with the same arguments.
func repeatWarning(name string, n int) string {
	return fmt.Sprintf("\n\n[Warning: this is call %d in a row to %s with the same arguments. Repeating it has not changed "+
		"anything and will not. Try something different, or stop and tell the user what is in the way.]", n, name)
}
