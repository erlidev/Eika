package utility

import (
	"slices"

	"github.com/erlidev/eika/internal/provider"
)

// Model is the model a task is sent to, as the user configured it.
type Model struct {
	// Provider is the client for the model's endpoint.
	Provider provider.Provider
	// ID is the endpoint's identifier for the model.
	ID string
	// MaxOutput is the most tokens one response of the model may have, zero
	// when the model sets no bound of its own.
	MaxOutput int
	// ReasoningEffort is the model's own effort, and ReasoningEfforts the
	// efforts it offers.
	ReasoningEffort  string
	ReasoningEfforts []string
	// ThinkingSwitch is the field that carries the effort none.
	ThinkingSwitch provider.ThinkingSwitch
}

// request builds a task's one request: the system prompt, the message, and
// at most maxOutput tokens of answer, with thinking off. Thinking goes off
// the way a run turns it off, with the effort none in the model's thinking
// switch; a model that does not offer none gets its own effort instead, as a
// run drops an effort its model does not offer, since an endpoint may refuse
// a field it does not know.
func (m Model) request(system, message string, maxOutput int) provider.Request {
	if m.MaxOutput > 0 {
		maxOutput = min(maxOutput, m.MaxOutput)
	}
	sampling := provider.Sampling{MaxOutput: &maxOutput}
	switch {
	case slices.Contains(m.ReasoningEfforts, provider.EffortNone):
		sampling.ReasoningEffort = new(provider.EffortNone)
	case m.ReasoningEffort != "":
		sampling.ReasoningEffort = new(m.ReasoningEffort)
	}
	return provider.Request{
		Model:          m.ID,
		System:         system,
		Messages:       []provider.Message{provider.UserMessage(message)},
		Sampling:       sampling,
		ThinkingSwitch: m.ThinkingSwitch,
	}
}
