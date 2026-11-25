// Package subagent owns named child sessions and their run lifecycle.
// It uses the session store and emits global session updates. New takes the
// application's run callback; Start, Message and Cancel control child runs.
// A child's run ends with a report, drafted on request or taken from its final
// message, and at most the configured number of children run at once. Board
// renders the children's rows as the board of a home compaction summary. Tool
// and server bindings are supplied by the caller.
package subagent
