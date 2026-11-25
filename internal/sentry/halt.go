package sentry

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// HaltState is whether Eika is halted. While it is, Decide denies every action.
type HaltState struct {
	Halted bool
	At     time.Time
	Reason string
}

// HaltStore keeps the halted state across restarts. Core supplies it
// (store.Store implements it). SetHalt writes the state and the audit row in
// one step.
type HaltStore interface {
	Halt(ctx context.Context) (HaltState, error)
	SetHalt(ctx context.Context, st HaltState, audit AuditEntry) error
}

// The audit events of a halt and its resume.
const (
	EventHalt   = "halt"
	EventResume = "resume"
)

// haltReason is what the model is told while halted.
const haltReason = "Eika is halted: the user stopped all work. Stop, tell the user you were blocked, and do not retry until they resume Eika"

// haltFlag is the gate's copy of the persisted state. It is read on every
// Decide, so it lives in memory; Halt and Resume write the store first.
type haltFlag struct {
	write sync.Mutex // serialises Halt and Resume
	mu    sync.RWMutex
	state HaltState
}

func (h *haltFlag) get() HaltState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state
}

func (h *haltFlag) set(st HaltState) {
	h.mu.Lock()
	h.state = st
	h.mu.Unlock()
}

// load reads the persisted state. A state that cannot be read counts as halted:
// a gate that cannot say whether the user stopped Eika must not allow work.
func (g *gate) load() {
	if g.Halts == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := g.Halts.Halt(ctx)
	if err != nil {
		g.Log.Error("read halt state; staying halted", "error", err)
		st = HaltState{Halted: true, At: time.Now().UTC(), Reason: "the halt state could not be read"}
	}
	g.halt.set(st)
}

func (g *gate) Halted() HaltState { return g.halt.get() }

// Halt records the halt and writes its audit row. It reports whether the state
// changed: halting a halted Eika changes nothing and writes nothing.
func (g *gate) Halt(ctx context.Context, reason string) (HaltState, bool, error) {
	return g.setHalt(ctx, HaltState{Halted: true, At: time.Now().UTC(), Reason: reason}, EventHalt)
}

// Resume clears the halt and writes its audit row. It reports whether the state
// changed.
func (g *gate) Resume(ctx context.Context) (HaltState, bool, error) {
	return g.setHalt(ctx, HaltState{}, EventResume)
}

func (g *gate) setHalt(ctx context.Context, next HaltState, event string) (HaltState, bool, error) {
	if g.Halts == nil {
		return HaltState{}, false, fmt.Errorf("sentry: no halt store")
	}
	g.halt.write.Lock()
	defer g.halt.write.Unlock()
	cur := g.halt.get()
	if cur.Halted == next.Halted {
		return cur, false, nil
	}
	err := g.Halts.SetHalt(ctx, next, AuditEntry{Event: event, Reason: next.Reason})
	if err != nil {
		return cur, false, fmt.Errorf("sentry: %s: %w", event, err)
	}
	g.halt.set(next)
	return next, true, nil
}
