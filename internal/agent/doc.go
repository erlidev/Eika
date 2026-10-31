// Package agent runs the loop that turns a user message into model responses
// and tool calls.
//
// One Agent drives one session at a time: it sends the conversation to a
// provider, streams the response, runs the tools the model asks for in order,
// appends the results, and calls the model again until it asks for no more
// tools. Steering messages join the conversation as soon as the running tool
// finishes; follow-up messages wait until the whole turn is done. Cancelling
// the context aborts the run and puts queued messages back.
//
// Every model request is assembled in one place, context.go, as a Context:
// the system prompt by named section (the base prompt and the extra
// instructions), the tool schemas, the messages as
// sent, and the parameters. Agent.Preview returns the same assembly for the
// next call without sending it, and a Recorder learns about every call a run
// makes.
//
// A conversation that outgrows the model's context window is compacted as Pi
// does it (compaction.go, summary.go): its older part is replaced by a summary
// the model writes, before a call that would pass the threshold, after an
// endpoint refuses a call as too large, or when Agent.Compact asks.
//
// The window is judged by the session's token counter (counting.go,
// provider/counter): every request is sized before it is sent, the size
// decides compaction and how much output is asked for, and the endpoint's own
// count is observed afterwards, so each recorded call carries its prediction
// and its drift. Counting is bounded by a timeout and never fails a call.
//
// Every model call, a compaction's summary included, waits for a lease on the
// inference queue (Options.Inference, inference.go): one lease per attempt,
// held for the model call alone and released on every way out. A retry takes a
// new lease, and an attempt that an interactive call preempted is retried
// without spending the retry budget. The call's wait, slot and priority are in
// the ModelCall the Recorder receives.
//
// The loop reports progress as event.Event values through an event.Emitter and
// persists every message through a Store. It depends on provider, tool,
// executor, event and inference, and on nothing above it.
//
// The entry points are New, Agent.Run, Agent.Compact, Agent.Preview,
// Agent.Steer, and Agent.FollowUp.
package agent
