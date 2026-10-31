// Package probe works out what kind of server an endpoint is and what it can
// take, from the server's own description of itself.
//
// Run asks an endpoint, route by route, which backend it is (llama.cpp, vLLM,
// SGLang, TabbyAPI, a generic cloud API or something else), how many requests
// it takes at once, how large one request's context is, whether its KV cache
// is shared among requests and how big that is, and which routes count tokens
// exactly. It reads documented routes of each project and verifies the token
// routes with one tiny request; it never sends a prompt to a model.
//
// CheckResponses is the one entry point that does. It sends the endpoint's model
// two small requests through a Responses provider (a streamed tool call with the
// model's reasoning, then that call, its result and the reasoning item sent back)
// and reports whether the Responses API works the way the loop needs: the
// Conformance it returns says so, with the model and server build it was made
// against, which is how a later probe knows whether to ask again. Each of its
// two requests takes a lease on the inference queue it is given, at the
// priority of whoever asked; a check that never gets one is skipped, not failed.
//
// The Result a probe returns is what the harness stores on a provider row. A
// field the user overrode is not replaced by it (internal/server and
// internal/store apply that rule), and a field the server does not state is
// left at its documented default or marked unknown rather than guessed.
//
// The entry point is Run. The fixtures in probetest say which responses were
// recorded from a live server and which were written from a project's
// documentation.
package probe
