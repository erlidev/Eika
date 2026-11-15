// Package subagent owns named child sessions and their run lifecycle.
// It uses the session store and emits global session updates. New takes the
// application's run callback; Start, Message and Cancel control child runs.
// Tool and server bindings are supplied by the caller.
package subagent
