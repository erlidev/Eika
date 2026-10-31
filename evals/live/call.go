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
	// Predicted is what the token counter said the prompt would be before
	// the request was sent, and Method how it reached that: exact, anchored
	// or estimated.
	Predicted *int   `json:"predicted_tokens"`
	Method    string `json:"prediction_method,omitempty"`
	// Drift is Predicted less PromptTokens. Any drift on an exact prediction
	// is a bug, and fails the scenario.
	Drift *int `json:"drift"`
	// CountSeconds is how long the prediction took, tokenizer requests
	// included: what counting adds before the model call.
	CountSeconds *float64 `json:"count_seconds"`
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
	method := c.Method
	if method == "" {
		method = "not predicted"
	}
	return fmt.Sprintf("%s: prompt %s tokens, cached %s, hit rate %s, prefill %s, wall %.2f s, predicted %s (%s), drift %s",
		c.Label, count(c.PromptTokens), count(c.CachedTokens), rate, prefill, c.WallSeconds, count(c.Predicted), method, count(c.Drift))
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
	// Drift is the sum of the absolute drift of all calls, so that errors in
	// opposite directions do not cancel. It is nil unless every call was
	// predicted and measured.
	Drift *int `json:"drift"`
	// CountSeconds is the time spent predicting the requests of all calls.
	CountSeconds *float64 `json:"count_seconds"`
}

// Summarise totals calls.
func Summarise(calls []Call) Summary {
	s := Summary{
		Calls:          len(calls),
		PromptTokens:   total(calls, func(c Call) *int { return c.PromptTokens }),
		CachedTokens:   total(calls, func(c Call) *int { return c.CachedTokens }),
		PrefillSeconds: total(calls, func(c Call) *float64 { return c.PrefillSeconds }),
		Drift:          total(calls, func(c Call) *int { return absolute(c.Drift) }),
		CountSeconds:   total(calls, func(c Call) *float64 { return c.CountSeconds }),
	}
	s.CacheHitRate = hitRate(s.CachedTokens, s.PromptTokens)
	return s
}

// drift states the drift, or "not measured".
func (c Call) drift() string {
	if c.Drift == nil {
		return "not measured"
	}
	return strconv.Itoa(*c.Drift)
}

// absolute returns the magnitude of a count that may not have been measured.
func absolute(p *int) *int {
	if p == nil {
		return nil
	}
	n := *p
	if n < 0 {
		n = -n
	}
	return &n
}

// exactDrift is the error a scenario fails with when a prediction called exact
// was not the server's count.
func exactDrift(calls []Call) error {
	for _, c := range calls {
		if c.Method == string(provider.MethodExact) && c.Drift != nil && *c.Drift != 0 {
			return fmt.Errorf("%s: the exact prediction was %d tokens and the server counted %d (drift %d): exact counting must not drift",
				c.Label, *c.Predicted, *c.PromptTokens, *c.Drift)
		}
	}
	return nil
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
