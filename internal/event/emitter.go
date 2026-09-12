package event

import "context"

// Emitter receives the events a run produces. The agent loop and the tools it
// runs hold one; the server implementation fans events out to subscribed
// clients. Emit never blocks the caller for long and never fails: an emitter
// that cannot deliver an event drops it.
type Emitter interface {
	Emit(ctx context.Context, e Event)
}

// EmitterFunc adapts a function to Emitter.
type EmitterFunc func(ctx context.Context, e Event)

// Emit calls f.
func (f EmitterFunc) Emit(ctx context.Context, e Event) { f(ctx, e) }

// Discard is an Emitter that drops every event. It is the default for a run
// nobody is watching.
var Discard Emitter = EmitterFunc(func(context.Context, Event) {})
