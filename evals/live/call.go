package live

import (
	"fmt"
	"strconv"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

// Call is what one model call measured. A pointer field is nil, and appears
// in the report as null or not at all, when nothing measured it: absent is
// never reported as zero. The counts are those usage parsing reads
// (provider.Usage); the suite derives none of them.
type Call struct {
	// Label names the call within its scenario, such as "turn 2".
	Label string `json:"label"`
	// PromptTokens is the size of the prompt the server counted, cached
	// tokens included.
	PromptTokens *int `json:"prompt_tokens"`
	// CompletionTokens is the tokens the model generated, reasoning included.
	CompletionTokens *int `json:"completion_tokens"`
	// ReasoningTokens is the part of CompletionTokens the server states was
	// reasoning. llama.cpp states none, so it is null there.
	ReasoningTokens *int `json:"reasoning_tokens"`
	// PrefilledTokens is the prompt tokens the server evaluated for this
	// call (llama.cpp's prompt_n).
	PrefilledTokens *int `json:"prefilled_tokens"`
	// CachedTokens is the prompt tokens the server did not evaluate because
	// its cache held them, as the server states it
	// (usage.prompt_tokens_details.cached_tokens, else timings.cache_n).
	CachedTokens *int `json:"cached_tokens"`
	// CacheHitRate is CachedTokens over PromptTokens, from 0 to 1.
	CacheHitRate *float64 `json:"cache_hit_rate"`
	// PrefillSeconds is how long the server spent evaluating the prompt, as
	// it measured it.
	PrefillSeconds *float64 `json:"prefill_seconds"`
	// WallSeconds is how long the call took from request to the end of the
	// stream, as the harness saw it.
	WallSeconds float64 `json:"wall_seconds"`
	// Drift is the exact counter's prediction for the prompt less
	// PromptTokens. Nothing counts yet, so it is nil: the token counter item
	// fills it in here, and Summarise totals it.
	Drift *int `json:"drift"`
	// StopReason is why the model stopped, such as stop or length.
	StopReason string `json:"stop_reason,omitempty"`
}

// String states the call on one line, with "n/a" for what nothing measured.
func (c Call) String() string {
	count := func(p *int) string {
		if p == nil {
			return "n/a"
		}
		return strconv.Itoa(*p)
	}
	rate, prefill := "n/a", "n/a"
	if c.CacheHitRate != nil {
		rate = fmt.Sprintf("%.1f%%", *c.CacheHitRate*100)
	}
	if c.PrefillSeconds != nil {
		prefill = fmt.Sprintf("%.3f s", *c.PrefillSeconds)
	}
	return fmt.Sprintf("%s: prompt %s tokens, cached %s, hit rate %s, prefill %s, wall %.2f s, drift %s",
		c.Label, count(c.PromptTokens), count(c.CachedTokens), rate, prefill, c.WallSeconds, count(c.Drift))
}

// newCall builds a Call from what the provider reported for one response.
func newCall(label string, usage provider.Usage, timings provider.Timings, stop string, wall time.Duration) Call {
	c := Call{
		Label:            label,
		PromptTokens:     usage.InputTokens.Ptr(),
		CompletionTokens: usage.OutputTokens.Ptr(),
		ReasoningTokens:  usage.ReasoningTokens.Ptr(),
		PrefilledTokens:  usage.PrefilledTokens.Ptr(),
		CachedTokens:     usage.CachedTokens.Ptr(),
		WallSeconds:      wall.Seconds(),
		StopReason:       stop,
	}
	c.CacheHitRate = hitRate(c.CachedTokens, c.PromptTokens)
	if timings.HasPrompt() {
		seconds := timings.PromptMS / 1000
		c.PrefillSeconds = &seconds
	}
	return c
}

// hitRate is cached over prompt, nil unless both were stated and the prompt
// was not empty.
func hitRate(cached, prompt *int) *float64 {
	if cached == nil || prompt == nil || *prompt <= 0 {
		return nil
	}
	rate := float64(*cached) / float64(*prompt)
	return &rate
}

// Summary totals the calls of a scenario. A total is nil unless every call
// measured it: a sum over calls where one did not state a count would read as
// the whole.
type Summary struct {
	// Calls is how many model calls the scenario made.
	Calls int `json:"calls"`
	// PromptTokens is the prompt tokens of all calls.
	PromptTokens *int `json:"prompt_tokens"`
	// CachedTokens is the cached tokens of all calls.
	CachedTokens *int `json:"cached_tokens"`
	// CacheHitRate is the cached tokens over the prompt tokens of all calls.
	CacheHitRate *float64 `json:"cache_hit_rate"`
	// PrefillSeconds is the prefill time of all calls.
	PrefillSeconds *float64 `json:"prefill_seconds"`
	// Drift is the drift of all calls, nil until the token counter exists.
	Drift *int `json:"drift"`
}

// Summarise totals calls.
func Summarise(calls []Call) Summary {
	s := Summary{
		Calls:          len(calls),
		PromptTokens:   total(calls, func(c Call) *int { return c.PromptTokens }),
		CachedTokens:   total(calls, func(c Call) *int { return c.CachedTokens }),
		PrefillSeconds: total(calls, func(c Call) *float64 { return c.PrefillSeconds }),
		Drift:          total(calls, func(c Call) *int { return c.Drift }),
	}
	s.CacheHitRate = hitRate(s.CachedTokens, s.PromptTokens)
	return s
}

// total adds up one measurement over calls, and is nil for no calls or when
// any call did not state it.
func total[N int | float64](calls []Call, of func(Call) *N) *N {
	if len(calls) == 0 {
		return nil
	}
	var sum N
	for _, c := range calls {
		v := of(c)
		if v == nil {
			return nil
		}
		sum += *v
	}
	return &sum
}
