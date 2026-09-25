// Package openai implements provider.Provider against any OpenAI-compatible
// Chat Completions endpoint.
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
// The entry point is New, which the provider registry calls for the "openai"
// kind.
package openai
