package provider

import (
	"errors"
	"fmt"
	"slices"
)

// Bounds on the stop sequences one request may carry. They keep a stored
// value and a request body small; no endpoint needs more.
const (
	// MaxStopSequences is the most stop sequences Sampling accepts.
	MaxStopSequences = 16
	// MaxStopSequenceLen is the most bytes one stop sequence may have.
	MaxStopSequenceLen = 256
)

// Sampling holds the parameters that steer how a model samples a response.
// Every field is optional: nil is the endpoint's default, and a request
// leaves that field out. It is a wire type and a stored value, so a field
// that is not set is also left out of its JSON, and the fields one layer of
// configuration sets are the keys of its encoding.
type Sampling struct {
	// Temperature scales the randomness of sampling, from 0 to 2.
	Temperature *float64 `json:"temperature,omitempty"`
	// TopP keeps the smallest set of tokens whose probability adds up to it,
	// from 0 to 1.
	TopP *float64 `json:"top_p,omitempty"`
	// TopK keeps the k most likely tokens. Chat Completions has no field for
	// it; the servers that run open models accept it beside the others.
	TopK *int `json:"top_k,omitempty"`
	// MinP drops the tokens less likely than this fraction of the most
	// likely one, from 0 to 1. Like TopK it is not a Chat Completions field.
	MinP *float64 `json:"min_p,omitempty"`
	// FrequencyPenalty discourages a token by how often it appeared, from -2
	// to 2.
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	// PresencePenalty discourages a token that appeared at all, from -2 to 2.
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`
	// Seed asks the endpoint to sample deterministically, where it can.
	Seed *int64 `json:"seed,omitempty"`
	// Stop lists sequences that end the response. Nil is the endpoint's
	// default; an empty list sends none, which is how an upper layer clears
	// the list of a lower one.
	Stop []string `json:"stop,omitzero"`
	// MaxOutput bounds the tokens one response may generate.
	MaxOutput *int `json:"max_output,omitempty"`
	// ReasoningEffort selects how much a reasoning model thinks. Its
	// vocabulary belongs to the endpoint, ValidReasoningEffort bounds its
	// shape, and EffortNone turns thinking off through the request's
	// ThinkingSwitch. The empty value leaves the choice to the endpoint.
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
}

// Over returns s layered over lower: every field s sets wins, and every
// field it leaves nil comes from lower. Configuration resolves as
// session.Over(profile).Over(model). The result shares no memory with
// either layer.
func (s Sampling) Over(lower Sampling) Sampling {
	stop := lower.Stop
	if s.Stop != nil {
		stop = s.Stop
	}
	return Sampling{
		Temperature:      layer(s.Temperature, lower.Temperature),
		TopP:             layer(s.TopP, lower.TopP),
		TopK:             layer(s.TopK, lower.TopK),
		MinP:             layer(s.MinP, lower.MinP),
		FrequencyPenalty: layer(s.FrequencyPenalty, lower.FrequencyPenalty),
		PresencePenalty:  layer(s.PresencePenalty, lower.PresencePenalty),
		Seed:             layer(s.Seed, lower.Seed),
		Stop:             slices.Clone(stop),
		MaxOutput:        layer(s.MaxOutput, lower.MaxOutput),
		ReasoningEffort:  layer(s.ReasoningEffort, lower.ReasoningEffort),
	}
}

// layer returns a copy of upper when it is set and of lower otherwise.
func layer[T any](upper, lower *T) *T {
	from := lower
	if upper != nil {
		from = upper
	}
	if from == nil {
		return nil
	}
	v := *from
	return &v
}

// Validate reports the first parameter no endpoint accepts, naming it by its
// JSON field so the message reads beside the control that set it. A nil
// field is always valid.
func (s Sampling) Validate() error {
	switch {
	case !within(s.Temperature, 0, 2):
		return errors.New("temperature must be from 0 to 2")
	case !within(s.TopP, 0, 1):
		return errors.New("top_p must be from 0 to 1")
	case s.TopK != nil && *s.TopK < 1:
		return errors.New("top_k must be at least 1")
	case !within(s.MinP, 0, 1):
		return errors.New("min_p must be from 0 to 1")
	case !within(s.FrequencyPenalty, -2, 2):
		return errors.New("frequency_penalty must be from -2 to 2")
	case !within(s.PresencePenalty, -2, 2):
		return errors.New("presence_penalty must be from -2 to 2")
	case len(s.Stop) > MaxStopSequences:
		return fmt.Errorf("stop holds at most %d sequences", MaxStopSequences)
	case s.MaxOutput != nil && *s.MaxOutput < 1:
		return errors.New("max_output must be at least 1 token")
	case s.ReasoningEffort != nil && !ValidReasoningEffort(*s.ReasoningEffort):
		return fmt.Errorf("reasoning_effort %q must be at most %d letters, digits, hyphens, or underscores",
			*s.ReasoningEffort, MaxReasoningEffortLen)
	}
	for _, seq := range s.Stop {
		if seq == "" || len(seq) > MaxStopSequenceLen {
			return fmt.Errorf("stop sequences must each be 1 to %d bytes", MaxStopSequenceLen)
		}
	}
	return nil
}

// within reports whether v is unset or between lo and hi inclusive. A NaN is
// outside every range.
func within(v *float64, lo, hi float64) bool {
	return v == nil || (*v >= lo && *v <= hi)
}
