// Package counter predicts how many tokens a request will cost before it is
// sent, and learns from what the server measured afterwards
// (docs/design/runtime.md 2).
//
// A Counter belongs to one session. Predict takes a provider.Request and
// returns a provider.Prediction with its method: exact when the endpoint
// renders and tokenises the request itself, anchored when it only tokenises
// text and the template's share is learned from measurements, estimated when
// it tokenises nothing. Observe takes the measured usage of that request,
// returns the drift, and trains the learned parts; the pair must be given the
// same request, which for this purpose is one with the same prompt (the most
// the model may generate is not part of it). Estimate answers by the estimated
// method without asking the server, for a caller out of time; Seed teaches a
// fresh counter from a measurement stored earlier; CountText counts a text of
// the caller's own. Size, ToolSizes and ImageSize size the parts of a request
// for the context breakdown and never scale one to fit a total, and Peek
// sizes a request for a preview without leaving a prediction for Observe. A Switch is a Source whose routes, and the key they carry,
// are attached only while a run uses them. Results are cached by the hash of
// what was counted, so a prefix is tokenised once per session.
//
// A Source is the endpoint's tokenizer routes. Routes reaches llama.cpp, vLLM,
// SGLang and TabbyAPI over HTTP and renders a chat request from the body its
// provider builds (provider.ChatEncoder), so the count is of what is sent. The
// package depends on provider and provider/probe.
package counter
