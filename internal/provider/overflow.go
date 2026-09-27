package provider

import (
	"regexp"
)

// overflowPatterns match the errors endpoints return for a request larger
// than the model's context window. They are Pi's list (pi-ai
// utils/overflow.ts), which names the endpoint each one comes from.
var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),                                                                        // Anthropic
	regexp.MustCompile(`(?i)request_too_large`),                                                                         // Anthropic, HTTP 413
	regexp.MustCompile(`(?i)input is too long for requested model`),                                                     // Amazon Bedrock
	regexp.MustCompile(`(?i)exceeds the context window`),                                                                // OpenAI
	regexp.MustCompile(`(?i)exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`), // LiteLLM, OpenAI-compatible
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),                                                    // Gemini
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),                                                              // xAI
	regexp.MustCompile(`(?i)reduce the length of the messages`),                                                         // Groq
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),                                                      // OpenRouter, vLLM
	regexp.MustCompile(`(?i)exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`),                           // OpenRouter
	regexp.MustCompile(`(?i)input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`),            // Together AI
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),                                                                  // GitHub Copilot
	regexp.MustCompile(`(?i)exceeds the available context size`),                                                        // llama.cpp
	regexp.MustCompile(`(?i)greater than the context length`),                                                           // LM Studio
	regexp.MustCompile(`(?i)context window exceeds limit`),                                                              // MiniMax
	regexp.MustCompile(`(?i)exceeded model token limit`),                                                                // Kimi
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`),                                       // Mistral
	regexp.MustCompile(`(?i)prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`),              // DS4
	regexp.MustCompile(`(?i)model_context_window_exceeded`),                                                             // z.ai
	regexp.MustCompile(`(?i)prompt too long; exceeded (?:max )?context length`),                                         // Ollama
	regexp.MustCompile(`(?i)range of input length should be`),                                                           // DashScope
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),
	regexp.MustCompile(`(?i)too many tokens`),
	regexp.MustCompile(`(?i)token limit exceeded`),
}

// notOverflowPatterns match errors that also match an overflow pattern but
// mean something else, such as Bedrock's "Too many tokens, please wait". They
// are Pi's, unanchored because an error here carries the operation that
// failed before the endpoint's message.
var notOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(Throttling error|Service unavailable):`),
	regexp.MustCompile(`(?i)please wait`),
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
}

// ContextOverflow reports whether err is an endpoint refusing a request
// because it does not fit the model's context window. Endpoints say so only
// in prose, so this matches the wording each one is known to use. A request
// that overflowed fails the same way however often it is sent.
func ContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	for _, p := range notOverflowPatterns {
		if p.MatchString(text) {
			return false
		}
	}
	for _, p := range overflowPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

// leadingReasoning is reasoning an endpoint left at the start of an answer's
// text, which it does when it does not parse the model's reasoning apart.
var leadingReasoning = regexp.MustCompile(`(?s)^\s*<think>.*?</think>`)

// StripReasoning returns an answer without the reasoning block an endpoint
// left at its start.
func StripReasoning(text string) string {
	return leadingReasoning.ReplaceAllString(text, "")
}
