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
// the system prompt by named section (the base prompt, the workspace's
// context files, the extra instructions), the tool schemas, the messages as
// sent, and the parameters. Agent.Preview returns the same assembly for the
// next call without sending it, and a Recorder learns about every call a run
// makes.
//
// The loop reports progress as event.Event values through an event.Emitter and
// persists every message through a Store. It depends on provider, tool,
// executor, contextfile, and event, and on nothing above it.
//
// The entry points are New, Agent.Run, Agent.Preview, Agent.Steer, and
// Agent.FollowUp.
package agent
