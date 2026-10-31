package provider

import (
	"bytes"
	"encoding/json"
	"math"
)

// Usage reports the tokens one model call cost, as the server stated them.
// A field the server did not state is not reported, never zero, and stays so
// through storage and the wire types to the UI. Each field means the same
// whatever protocol it came from, which is what lets the accounting code read
// them without asking which backend answered:
//
//   - InputTokens is the whole prompt as the server tokenised it, the cached
//     part included. It is the server's own count, so the context meter and
//     every prediction are measured against it.
//   - OutputTokens is everything generated, reasoning included.
//   - TotalTokens is what the server states as the sum of the two, the
//     window fill after the call.
//   - CachedTokens is the part of InputTokens the server reused from its cache
//     instead of processing.
//   - PrefilledTokens is the part of InputTokens the server processed, where
//     it states that itself (llama.cpp's prompt_n).
//   - ReasoningTokens is the part of OutputTokens that was reasoning.
//
// "Includes the cached part" was checked against llama.cpp on both its routes
// (docs/design/runtime.md section 2, Terms). The other backends are read by
// their documented meaning, which is the same for the OpenAI-shaped fields,
// and are marked unverified there until a server of that kind is on the bench.
type Usage struct {
	InputTokens     Count `json:"input_tokens"`
	OutputTokens    Count `json:"output_tokens"`
	TotalTokens     Count `json:"total_tokens"`
	CachedTokens    Count `json:"cached_tokens"`
	PrefilledTokens Count `json:"prefilled_tokens"`
	ReasoningTokens Count `json:"reasoning_tokens"`
}

// Reported reports whether the usage states any count at all.
func (u Usage) Reported() bool { return u != Usage{} }

// Prefilled returns the tokens of the prompt the server processed: the count
// it states itself (llama.cpp's prompt_n), else the prompt less its cached
// part when it states both, which is the same number because the prompt
// includes the cached part. It is not reported when the cached part is
// unknown, since the prompt alone does not say what was processed.
func (u Usage) Prefilled() Count {
	if u.PrefilledTokens.Reported() {
		return u.PrefilledTokens
	}
	prompt, haveIn := u.InputTokens.Value()
	cached, haveCached := u.CachedTokens.Value()
	if !haveIn || !haveCached || cached > prompt {
		return Count{}
	}
	return Reported(prompt - cached)
}

// UnmarshalJSON reads a usage, including the form stored before a field could
// be unreported. That form has no cached_tokens key, and wrote 0 for a count
// the server did not state; a usage of three zeros is one no real call has,
// since a prompt always has tokens, so it reads as nothing reported.
func (u *Usage) UnmarshalJSON(data []byte) error {
	type plain Usage
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err == nil {
		_, current := keys["cached_tokens"]
		allZero := p.InputTokens == Reported(0) && p.OutputTokens == Reported(0) && p.TotalTokens == Reported(0)
		if !current && allZero {
			p = plain{}
		}
	}
	*u = Usage(p)
	return nil
}

// Add returns the field-wise sum of two usages. A field not reported by
// either one is not reported in the sum. Summing a run of calls is Tally's
// job, which does not let an empty start poison the result.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:     u.InputTokens.Plus(o.InputTokens),
		OutputTokens:    u.OutputTokens.Plus(o.OutputTokens),
		TotalTokens:     u.TotalTokens.Plus(o.TotalTokens),
		CachedTokens:    u.CachedTokens.Plus(o.CachedTokens),
		PrefilledTokens: u.PrefilledTokens.Plus(o.PrefilledTokens),
		ReasoningTokens: u.ReasoningTokens.Plus(o.ReasoningTokens),
	}
}

// Merge returns u with every field o reports replaced by o's. A server that
// states its usage over several chunks says each count once or repeats it;
// either way the newest statement wins and a count it does not repeat stays.
func (u Usage) Merge(o Usage) Usage {
	pick := func(old, now Count) Count {
		if now.Reported() {
			return now
		}
		return old
	}
	return Usage{
		InputTokens:     pick(u.InputTokens, o.InputTokens),
		OutputTokens:    pick(u.OutputTokens, o.OutputTokens),
		TotalTokens:     pick(u.TotalTokens, o.TotalTokens),
		CachedTokens:    pick(u.CachedTokens, o.CachedTokens),
		PrefilledTokens: pick(u.PrefilledTokens, o.PrefilledTokens),
		ReasoningTokens: pick(u.ReasoningTokens, o.ReasoningTokens),
	}
}

// Tally adds up the usage of a run of model calls. The first call sets the
// total, so an empty tally is "no call yet" rather than a reported zero, and
// each later call is added with Usage.Add: a count that any one call did not
// report is not reported in the total. The total is therefore always the sum
// of what every call said, never a sum with holes that reads as complete.
type Tally struct {
	total Usage
	calls int
}

// Add folds the usage of one finished call into the tally.
func (t *Tally) Add(u Usage) {
	if t.calls == 0 {
		t.total = u
	} else {
		t.total = t.total.Add(u)
	}
	t.calls++
}

// AddTally folds in a finished run of calls as one more call whose usage is
// that run's total. A tally that has added no call adds nothing.
func (t *Tally) AddTally(o Tally) {
	if o.calls > 0 {
		t.Add(o.total)
	}
}

// Total returns the tally's total, with nothing reported before the first call.
func (t Tally) Total() Usage { return t.total }

// Calls returns how many calls the tally has added.
func (t Tally) Calls() int { return t.calls }

// ChatUsage reads what one Chat Completions stream chunk (or a whole response
// body) states about the tokens the call cost. It reports false when the chunk
// states none, which is the common case: most chunks carry no usage.
//
// The fields read, with where each backend puts them:
//
//	usage.prompt_tokens                              InputTokens
//	usage.completion_tokens                          OutputTokens
//	usage.total_tokens                               TotalTokens
//	usage.prompt_tokens_details.cached_tokens        CachedTokens (preferred)
//	timings.cache_n                                  CachedTokens (llama.cpp, fallback)
//	timings.prompt_n                                 PrefilledTokens (llama.cpp)
//	usage.completion_tokens_details.reasoning_tokens ReasoningTokens
//
// Two fields can state the cached count, and the usage one is preferred. It
// is the field the OpenAI API defines and the one vLLM, SGLang and the cloud
// APIs carry, so the meaning of a cached count does not depend on which
// backend answered. cache_n exists only in llama.cpp, which is the fallback
// for a build that predates the usage field. On llama.cpp b11942 the two are
// equal, so the choice changes no number there.
//
// llama.cpp states no reasoning count on this route, so ReasoningTokens is not
// reported for it. Nothing here estimates one from the reasoning text.
func ChatUsage(chunk []byte) (Usage, bool) {
	var w struct {
		Usage *struct {
			PromptTokens        *float64 `json:"prompt_tokens"`
			CompletionTokens    *float64 `json:"completion_tokens"`
			TotalTokens         *float64 `json:"total_tokens"`
			PromptTokensDetails *struct {
				CachedTokens *float64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokensDetails *struct {
				ReasoningTokens *float64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Timings *promptTimings `json:"timings"`
	}
	if !decodeUsage(chunk, &w) {
		return Usage{}, false
	}
	var in, out, total, cached, reasoning *float64
	if u := w.Usage; u != nil {
		in, out, total = u.PromptTokens, u.CompletionTokens, u.TotalTokens
		if u.PromptTokensDetails != nil {
			cached = u.PromptTokensDetails.CachedTokens
		}
		if u.CompletionTokensDetails != nil {
			reasoning = u.CompletionTokensDetails.ReasoningTokens
		}
	}
	return buildUsage(in, out, total, cached, reasoning, w.Timings)
}

// ResponsesUsage reads what a Responses API event states about the tokens the
// call cost: the response.completed event (also response.incomplete) of a
// stream, whose response object carries the usage. It reports false when the
// event states none.
//
// The fields read:
//
//	response.usage.input_tokens                            InputTokens
//	response.usage.output_tokens                           OutputTokens
//	response.usage.total_tokens                            TotalTokens
//	response.usage.input_tokens_details.cached_tokens      CachedTokens (preferred)
//	timings.cache_n                                        CachedTokens (llama.cpp, fallback)
//	timings.prompt_n                                       PrefilledTokens (llama.cpp)
//	response.usage.output_tokens_details.reasoning_tokens  ReasoningTokens
//
// llama.cpp puts timings beside the response object, at the top of the event,
// not inside it. The preference between the two cached counts is ChatUsage's,
// for the same reason. llama.cpp b11942 states no reasoning count on this
// route either, so ReasoningTokens is not reported for it.
func ResponsesUsage(event []byte) (Usage, bool) {
	var w struct {
		Response *struct {
			Usage *struct {
				InputTokens        *float64 `json:"input_tokens"`
				OutputTokens       *float64 `json:"output_tokens"`
				TotalTokens        *float64 `json:"total_tokens"`
				InputTokensDetails *struct {
					CachedTokens *float64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputTokensDetails *struct {
					ReasoningTokens *float64 `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
		Timings *promptTimings `json:"timings"`
	}
	if !decodeUsage(event, &w) {
		return Usage{}, false
	}
	var in, out, total, cached, reasoning *float64
	if w.Response != nil && w.Response.Usage != nil {
		u := w.Response.Usage
		in, out, total = u.InputTokens, u.OutputTokens, u.TotalTokens
		if u.InputTokensDetails != nil {
			cached = u.InputTokensDetails.CachedTokens
		}
		if u.OutputTokensDetails != nil {
			reasoning = u.OutputTokensDetails.ReasoningTokens
		}
	}
	return buildUsage(in, out, total, cached, reasoning, w.Timings)
}

// promptTimings is the part of llama.cpp's timings block that counts tokens:
// the prompt tokens served from its cache and the ones it processed.
type promptTimings struct {
	CacheN  *float64 `json:"cache_n"`
	PromptN *float64 `json:"prompt_n"`
}

// decodeUsage decodes raw into w, and reports false when raw is not a JSON
// object of the shape w describes.
func decodeUsage(raw []byte, w any) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return false
	}
	return json.Unmarshal(raw, w) == nil
}

// buildUsage assembles a Usage from the numbers a response stated, each nil
// when absent. A usage block whose three headline counts are all zero or
// missing states nothing about this call: OpenAI-compatible gateways send one
// as a placeholder in chunks before the last, and no call has an empty
// prompt, since the template alone adds tokens. Its detail fields are ignored
// with it. A zero beside real counts is a statement and stays zero.
func buildUsage(in, out, total, cached, reasoning *float64, t *promptTimings) (Usage, bool) {
	var u Usage
	if positive(in) || positive(out) || positive(total) {
		u = Usage{
			InputTokens:     count(in),
			OutputTokens:    count(out),
			TotalTokens:     count(total),
			CachedTokens:    count(cached),
			ReasoningTokens: count(reasoning),
		}
	}
	if t != nil {
		if !u.CachedTokens.Reported() {
			u.CachedTokens = count(t.CacheN)
		}
		u.PrefilledTokens = count(t.PromptN)
	}
	return u, u.Reported()
}

// positive reports whether the server stated a number above zero.
func positive(f *float64) bool { return count(f).Or(0) > 0 }

// count turns a number the server stated into a Count. A missing number, and
// one that is no token count (negative, fractional, huge), is not reported:
// guessing at a repair would be inventing a statement.
func count(f *float64) Count {
	if f == nil || *f < 0 || *f != math.Trunc(*f) || *f > math.MaxInt32 {
		return Count{}
	}
	return Reported(int(*f))
}
