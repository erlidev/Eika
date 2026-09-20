// Package provider is the boundary between the agent loop and a language
// model.
//
// A Provider turns a Request into a stream of Events: text and reasoning
// deltas, tool calls, token usage, and a terminal done or error event. The
// agent loop consumes that stream and never sees a vendor SDK type.
// Conversation messages, tool definitions, and usage live here because both
// the loop and every provider implementation need them.
//
// The entry points are Provider, Request, Event, and Registry, which maps the
// kind a provider row names to a constructor taking that row's Endpoint.
// Lister is the optional interface of a provider that can list its models.
// The only implementation today is provider/openai.
package provider
