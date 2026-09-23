package openai

import (
	"encoding/json"

	"github.com/erlidev/eika/internal/provider"
)

// "OpenAI-compatible" covers the request shape, not the response: every
// engine that measures its own speed invented its own place to report it, and
// none of them is in the OpenAI specification. The chunks the SDK decodes
// therefore carry these numbers as unknown fields, and this file reads them
// out of the raw JSON.
//
// The engines and the shapes they send, in the order they are preferred:
//
//	llama.cpp   timings{prompt_n, prompt_ms, prompt_per_second,
//	            predicted_n, predicted_ms, predicted_per_second}
//	vLLM        metrics{time_to_first_token_ms, queue_time_ms,
//	            generation_time_ms}, with --enable-per-request-metrics
//	NVIDIA NIM  stats{llm_input_token_length, time_in_queue_in_ms,
//	            response_tokens{response_token_length,
//	            time_to_first_token_in_ms, tokens_per_second}}
//	LM Studio   stats{time_to_first_token, generation_time,
//	            tokens_per_second}, in seconds
//	TabbyAPI    usage{prompt_tokens_per_sec, completion_tokens_per_sec}
//	Groq        x_groq.usage{prompt_time, completion_time}, in seconds
//	Ollama      prompt_eval_count, prompt_eval_duration, eval_count,
//	            eval_duration, in nanoseconds
//
// The rest — OpenAI, Anthropic, OpenRouter, SGLang, Together — report nothing
// about their speed in the stream, and the agent times those itself.

// timingsWire is every one of those shapes in one struct: a field nobody
// sends decodes to zero, so one pass over the chunk reads them all. The
// counts are float64 because an endpoint is free to send 12.0 for twelve.
type timingsWire struct {
	Timings struct {
		PromptN            float64 `json:"prompt_n"`
		PromptMS           float64 `json:"prompt_ms"`
		PromptPerSecond    float64 `json:"prompt_per_second"`
		PredictedN         float64 `json:"predicted_n"`
		PredictedMS        float64 `json:"predicted_ms"`
		PredictedPerSecond float64 `json:"predicted_per_second"`
	} `json:"timings"`

	Metrics struct {
		TimeToFirstTokenMS float64 `json:"time_to_first_token_ms"`
		QueueTimeMS        float64 `json:"queue_time_ms"`
		GenerationTimeMS   float64 `json:"generation_time_ms"`
	} `json:"metrics"`

	Stats struct {
		// NVIDIA NIM, in milliseconds.
		InputTokenLength   float64 `json:"llm_input_token_length"`
		TimeInQueueInMS    float64 `json:"time_in_queue_in_ms"`
		GenerationTimeInMS float64 `json:"generation_time_in_ms"`
		ResponseTokens     struct {
			Length               float64 `json:"response_token_length"`
			TimeToFirstTokenInMS float64 `json:"time_to_first_token_in_ms"`
			TokensPerSecond      float64 `json:"tokens_per_second"`
		} `json:"response_tokens"`
		// LM Studio, in seconds.
		TimeToFirstToken float64 `json:"time_to_first_token"`
		GenerationTime   float64 `json:"generation_time"`
		TokensPerSecond  float64 `json:"tokens_per_second"`
	} `json:"stats"`

	Usage usageWire `json:"usage"`
	XGroq struct {
		Usage usageWire `json:"usage"`
	} `json:"x_groq"`

	PromptEvalCount    float64 `json:"prompt_eval_count"`
	PromptEvalDuration float64 `json:"prompt_eval_duration"`
	EvalCount          float64 `json:"eval_count"`
	EvalDuration       float64 `json:"eval_duration"`
}

// usageWire is the extra speed data two engines hang off the usage block.
type usageWire struct {
	PromptTokens     float64 `json:"prompt_tokens"`
	CompletionTokens float64 `json:"completion_tokens"`
	// TabbyAPI, as rates.
	PromptTokensPerSec     float64 `json:"prompt_tokens_per_sec"`
	CompletionTokensPerSec float64 `json:"completion_tokens_per_sec"`
	// Groq, as phase times in seconds.
	PromptTime     float64 `json:"prompt_time"`
	CompletionTime float64 `json:"completion_time"`
}

// nanosecondsPerMS and secondsPerMS convert the units the engines above use.
const (
	nanosecondsPerMS = 1e6
	msPerSecond      = 1e3
)

// parseTimings reads what one streamed chunk says about the endpoint's own
// speed. usage supplies the token counts for an engine that reports a rate or
// a duration without the tokens it applies to; the chunk's own usage block is
// the fallback, because a rate that arrives in a chunk with no usage would
// otherwise be unusable. It reports false when the chunk says nothing this
// code understands, which is the common case: most chunks say nothing, and
// most endpoints never do.
func parseTimings(raw string, usage provider.Usage) (provider.Timings, bool) {
	var w timingsWire
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return provider.Timings{}, false
	}
	in, out := float64(usage.InputTokens), float64(usage.OutputTokens)
	for _, u := range []usageWire{w.Usage, w.XGroq.Usage} {
		if in == 0 {
			in = u.PromptTokens
		}
		if out == 0 {
			out = u.CompletionTokens
		}
	}

	var t provider.Timings
	// Each engine is offered both phases in turn, and the first to measure a
	// phase keeps it: a gateway that passes another engine's numbers through
	// can leave a request carrying two shapes, one of them stale.
	phase := func(tokens, ms float64, into *int, intoMS *float64) {
		if *into > 0 || tokens < 1 || ms <= 0 {
			return
		}
		*into, *intoMS = int(tokens), ms
	}
	prompt := func(tokens, ms float64) { phase(tokens, ms, &t.PromptTokens, &t.PromptMS) }
	decode := func(tokens, ms float64) { phase(tokens, ms, &t.DecodeTokens, &t.DecodeMS) }

	// llama.cpp states both phases outright, and states the rate as well, so
	// a build that reports one without the other still yields a duration.
	prompt(w.Timings.PromptN, orRate(w.Timings.PromptMS, w.Timings.PromptN, w.Timings.PromptPerSecond))
	decode(w.Timings.PredictedN, orRate(w.Timings.PredictedMS, w.Timings.PredictedN, w.Timings.PredictedPerSecond))

	// vLLM's generation time runs from the first output token to the last, so
	// the tokens it covers are the ones after the first. What is left of the
	// time to first token once the queue wait comes off is the prefill.
	prompt(in, w.Metrics.TimeToFirstTokenMS-w.Metrics.QueueTimeMS)
	decode(out-1, w.Metrics.GenerationTimeMS)

	// NIM reports the generation phase as a rate over its own token count.
	nim := w.Stats.ResponseTokens
	prompt(orCount(w.Stats.InputTokenLength, in), w.Stats.ResponseTokens.TimeToFirstTokenInMS-w.Stats.TimeInQueueInMS)
	decode(orCount(nim.Length, out), orRate(w.Stats.GenerationTimeInMS, orCount(nim.Length, out), nim.TokensPerSecond))

	// LM Studio counts in seconds and has no queue to subtract.
	prompt(in, w.Stats.TimeToFirstToken*msPerSecond)
	decode(out, orRate(w.Stats.GenerationTime*msPerSecond, out, w.Stats.TokensPerSecond))

	// TabbyAPI and Groq both use the usage block, one with rates and one with
	// durations in seconds. Groq streams it under x_groq and returns it under
	// usage, so both places are read.
	for _, u := range []usageWire{w.Usage, w.XGroq.Usage} {
		prompt(in, orRate(u.PromptTime*msPerSecond, in, u.PromptTokensPerSec))
		decode(out, orRate(u.CompletionTime*msPerSecond, out, u.CompletionTokensPerSec))
	}

	// Ollama's native durations, in nanoseconds, as some gateways pass them
	// through beside an OpenAI-shaped body.
	prompt(orCount(w.PromptEvalCount, in), w.PromptEvalDuration/nanosecondsPerMS)
	decode(orCount(w.EvalCount, out), w.EvalDuration/nanosecondsPerMS)

	if !t.Known() {
		return provider.Timings{}, false
	}
	if t.HasDecode() {
		t.Source = provider.TimedByEndpoint
	}
	return t, true
}

// orRate returns ms when the endpoint stated a duration, and otherwise the
// duration that many tokens take at the stated rate.
func orRate(ms, tokens, perSecond float64) float64 {
	if ms > 0 {
		return ms
	}
	if tokens <= 0 || perSecond <= 0 {
		return 0
	}
	return tokens / perSecond * msPerSecond
}

// orCount returns the engine's own token count, falling back to the usage
// block's when the engine did not state one.
func orCount(own, usage float64) float64 {
	if own > 0 {
		return own
	}
	return usage
}
