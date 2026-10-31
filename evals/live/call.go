package live

import (
	"fmt"
	"strconv"
	"time"

	"github.com/erlidev/eika/internal/provider"
)

// Call is what one model call measured. A pointer field is nil, and appears
// in the report as null or not at all, when nothing measured it: absent is
// never reported as zero.
type Call struct {
	// Label names the call within its scenario, such as "turn 2".
	Label string `json:"label"`
	// PromptTokens is the size of the prompt the server counted, cached
	// tokens included.
	PromptTokens int `json:"prompt_tokens"`
	// CompletionTokens is the tokens the model generated.
	CompletionTokens int `json:"completion_tokens"`
	// PrefilledTokens is the prompt tokens the server evaluated for this
	// call (llama.cpp's prompt_n), as the provider read them from the
	// stream's timings.
	PrefilledTokens *int `json:"prefilled_tokens"`
	// CachedTokens is the prompt tokens the server did not evaluate because
	// its cache held them: PromptTokens less PrefilledTokens. llama.cpp
	// counts them in prompt_tokens (checked against its own cache_n on
	// b11942), which is what makes the subtraction right. Nothing else on
	// the bench reports it that way, so the provider's usage parsing, when it
	// reads the cached count itself, replaces this derivation.
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
	cached, rate, prefill, drift := "n/a", "n/a", "n/a", "n/a"
	if c.CachedTokens != nil {
		cached = strconv.Itoa(*c.CachedTokens)
	}
	if c.CacheHitRate != nil {
		rate = fmt.Sprintf("%.1f%%", *c.CacheHitRate*100)
	}
	if c.PrefillSeconds != nil {
		prefill = fmt.Sprintf("%.3f s", *c.PrefillSeconds)
	}
	if c.Drift != nil {
		drift = strconv.Itoa(*c.Drift)
	}
	return fmt.Sprintf("%s: prompt %d tokens, cached %s, hit rate %s, prefill %s, wall %.2f s, drift %s",
		c.Label, c.PromptTokens, cached, rate, prefill, c.WallSeconds, drift)
}

// newCall builds a Call from what the provider reported for one response.
func newCall(label string, usage provider.Usage, timings provider.Timings, stop string, wall time.Duration) Call {
	c := Call{
		Label:            label,
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		WallSeconds:      wall.Seconds(),
		StopReason:       stop,
	}
	if timings.HasPrompt() {
		prefilled := timings.PromptTokens
		seconds := timings.PromptMS / 1000
		c.PrefilledTokens, c.PrefillSeconds = &prefilled, &seconds
		if cached := usage.InputTokens - prefilled; cached >= 0 && usage.InputTokens > 0 {
			rate := float64(cached) / float64(usage.InputTokens)
			c.CachedTokens, c.CacheHitRate = &cached, &rate
		}
	}
	return c
}

// Summary totals the calls of a scenario. A total is nil unless every call
// measured it.
type Summary struct {
	// Calls is how many model calls the scenario made.
	Calls int `json:"calls"`
	// PromptTokens is the prompt tokens of all calls.
	PromptTokens int `json:"prompt_tokens"`
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
	s := Summary{Calls: len(calls)}
	cached, seconds, drift := 0, 0.0, 0
	haveCached, haveSeconds, haveDrift := len(calls) > 0, len(calls) > 0, len(calls) > 0
	for _, c := range calls {
		s.PromptTokens += c.PromptTokens
		if c.CachedTokens == nil {
			haveCached = false
		} else {
			cached += *c.CachedTokens
		}
		if c.PrefillSeconds == nil {
			haveSeconds = false
		} else {
			seconds += *c.PrefillSeconds
		}
		if c.Drift == nil {
			haveDrift = false
		} else {
			drift += *c.Drift
		}
	}
	if haveCached {
		s.CachedTokens = &cached
		if s.PromptTokens > 0 {
			rate := float64(cached) / float64(s.PromptTokens)
			s.CacheHitRate = &rate
		}
	}
	if haveSeconds {
		s.PrefillSeconds = &seconds
	}
	if haveDrift {
		s.Drift = &drift
	}
	return s
}
