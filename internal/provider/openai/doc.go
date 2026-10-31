// Package openai implements provider.Provider against any OpenAI-compatible
// Chat Completions endpoint, and, as Responses, its Responses API.
//
// It uses the official openai-go SDK in streaming mode with tool calling, and
// translates SDK chunks into provider.Event values so that nothing above this
// package sees an SDK type. The base URL and API key come from a
// provider.Endpoint the harness builds from the user's provider settings; the
// model is named per request. Models lists what the endpoint serves, with the
// context sizes compatible endpoints report, for the setup screens.
//
// It sends the standard reasoning_effort field. For compatible endpoints it
// also sends preserve_thinking and maps streamed and replayed reasoning to
// reasoning_content. Those two extension fields are not part of the OpenAI
// Chat Completions contract. The effort "none" goes in the field the
// request's ThinkingSwitch names: reasoning_effort, or instead the
// chat_template_kwargs or thinking extension, never more than one, because an
// endpoint may reject a field it does not know.
//
// Usage is read from each chunk by provider.ChatUsage: the prompt, completion
// and total counts, the cached and reasoning counts where the endpoint states
// them, and llama.cpp's prompt_n. A count the endpoint does not state stays
// unreported. timings.go reads how fast the endpoint ran, a separate matter.
//
// ChatBody returns the JSON body Stream would post, from the same code, so the
// token counter can have a server render exactly what the model is sent.
//
// Responses is the second implementation, for the stateless Responses API
// (store false, the whole input on every call). It wraps Provider and shares its
// client, key rules, error classification, model list and ChatBody, so the loop,
// the accounting and the compaction do not see which protocol is in use. Its
// stream is read as raw server-sent events: a reasoning item is kept exactly as
// the endpoint returned it (provider.KindReasoningItem, stored as
// Message.ReasoningItems) and sent back as it was. Endpoint.Hosted says a hosted
// API, which takes the request fields the specification defines and no others.
//
// The entry point is New, which the provider registry calls for the "openai"
// kind.
package openai
