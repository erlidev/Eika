// Package trip is the chain engine of the tripwires (docs/design/sentry.md,
// section 11). A mark does nothing alone; a tripwire fires when the marks of
// one run join into its chain. The engine holds each run's marks in memory and
// answers a chain question in a few comparisons, so Decide can ask it on every
// outbound call. It knows nothing of Core: the gate in internal/sentry feeds
// it marks and acts on what it answers.
package trip

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Kind names a mark. The values are those of the `marks` table.
type Kind string

// The marks the chains read.
const (
	Watched       Kind = "watched"
	Hidden        Kind = "hidden"
	SensitiveRead Kind = "sensitive-read"
	Carried       Kind = "carried"
	NewSink       Kind = "new-sink"
	ToldSink      Kind = "told-sink"
	Upload        Kind = "upload"
	FanOut        Kind = "fan-out"
)

// Mark is one mark of a run. Call is the tool call or connection that
// produced it, so a chain "on one call" joins on it; Computer joins the
// chains that name one computer.
type Mark struct {
	Kind     Kind
	Call     string
	Computer string
}

// Tripwire names a chain.
type Tripwire string

// The tripwires built on marks. Canary is not a chain and lives in the gate.
const (
	Followed     Tripwire = "followed"
	CarriedOut   Tripwire = "carried_out"
	Lifted       Tripwire = "lifted"
	Drained      Tripwire = "drained"
	BlockedOften Tripwire = "blocked_often"
)

// Settings are the per-tripwire switches and numbers (the `tripwires`
// setting). The zero value, and a deployment that stores nothing, has every
// tripwire off: they ship switched off until the injection report turns on
// those that pass it.
type Settings struct {
	Followed     bool `json:"followed"`
	CarriedOut   bool `json:"carried_out"`
	Lifted       bool `json:"lifted"`
	Drained      bool `json:"drained"`
	BlockedOften bool `json:"blocked_often"`
	// BlockedLimit is the number of blocked results in one run that fires
	// Blocked often.
	BlockedLimit int `json:"blocked_limit"`
}

// DefaultBlockedLimit is the starting value of Settings.BlockedLimit.
const DefaultBlockedLimit = 10

const maxBlockedLimit = 10_000

// On reports whether a tripwire's switch is on.
func (s Settings) On(t Tripwire) bool {
	switch t {
	case Followed:
		return s.Followed
	case CarriedOut:
		return s.CarriedOut
	case Lifted:
		return s.Lifted
	case Drained:
		return s.Drained
	case BlockedOften:
		return s.BlockedOften
	}
	return false
}

// Validate checks a value of the setting.
func (s Settings) Validate() error {
	if s.BlockedLimit < 1 || s.BlockedLimit > maxBlockedLimit {
		return fmt.Errorf("blocked_limit must be 1 to %d", maxBlockedLimit)
	}
	return nil
}

// Parse reads a stored value; a field it leaves out is off, and the limit
// keeps its default.
func Parse(raw json.RawMessage) (Settings, error) {
	s := Settings{BlockedLimit: DefaultBlockedLimit}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, err
	}
	return s, s.Validate()
}

// Bounds on what the engine keeps. A mark lost to a bound can only keep a
// chain from firing, never make one fire.
const (
	maxRuns      = 256
	maxRunMarks  = 512
	maxBlockedCt = 1 << 30
)

// Engine holds the marks of the runs in progress. It is safe for concurrent use.
type Engine struct {
	mu   sync.Mutex
	runs map[string]*run
}

type run struct {
	marks   []Mark
	blocked int
}

func (e *Engine) get(id string) *run {
	r := e.runs[id]
	if r == nil {
		if len(e.runs) >= maxRuns {
			return nil
		}
		if e.runs == nil {
			e.runs = map[string]*run{}
		}
		r = &run{}
		e.runs[id] = r
	}
	return r
}

// Add notes a mark of a run.
func (e *Engine) Add(runID string, m Mark) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r := e.get(runID); r != nil && len(r.marks) < maxRunMarks {
		r.marks = append(r.marks, m)
	}
}

// Blocked notes one blocked result in a run and returns how many it has.
func (e *Engine) Blocked(runID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.get(runID)
	if r == nil {
		return 0
	}
	if r.blocked < maxBlockedCt {
		r.blocked++
	}
	return r.blocked
}

// End forgets a run.
func (e *Engine) End(runID string) {
	e.mu.Lock()
	delete(e.runs, runID)
	e.mu.Unlock()
}

// Check returns the first enabled tripwire the marks of a run complete at call
// on computer, in the order of the design's table. The marks of the call
// itself must have been added first.
//
//   - Followed: a watched mark on this call.
//   - CarriedOut: carried and new-sink, both on this call.
//   - Lifted: told-sink on this call, and a sensitive-read on the same computer
//     before it.
//   - Drained: upload or fan-out on this call, with no source or access mark
//     (watched, hidden, sensitive-read, carried) anywhere in the run. Those
//     chains tell a stronger story and are not drained quietly.
//   - BlockedOften is not a chain over marks: see Settings.BlockedLimit and
//     Engine.Blocked.
func (e *Engine) Check(s Settings, runID, call, computer string) (Tripwire, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.runs[runID]
	if r == nil {
		return "", false
	}
	onCall := map[Kind]bool{}
	var other, sensitiveBefore bool
	for _, m := range r.marks {
		if m.Call == call {
			onCall[m.Kind] = true
		}
		switch m.Kind {
		case SensitiveRead:
			// Marks are in the order they were added, so one seen before the
			// call's told-sink came first.
			sensitiveBefore = sensitiveBefore || (m.Computer == computer && !onCall[ToldSink])
			other = true
		case Watched, Hidden, Carried:
			other = true
		}
	}
	switch {
	case s.Followed && onCall[Watched]:
		return Followed, true
	case s.CarriedOut && onCall[Carried] && onCall[NewSink]:
		return CarriedOut, true
	case s.Lifted && onCall[ToldSink] && sensitiveBefore:
		return Lifted, true
	case s.Drained && (onCall[Upload] || onCall[FanOut]) && !other:
		return Drained, true
	}
	return "", false
}
