// Package openai implements provider.Provider against any OpenAI-compatible
// Chat Completions endpoint.
//
// It uses the official openai-go SDK in streaming mode with tool calling, and
// translates SDK chunks into provider.Event values so that nothing above this
// package sees an SDK type. Base URL, model name, and the name of the
// environment variable holding the API key come from config.Model; the key is
// resolved once in New.
//
// The entry point is New, which the provider registry calls for the "openai"
// kind.
package openai
