//go:build docker

package server_test

import "github.com/erlidev/eika/internal/agent"

type modelCallWire struct {
	ID               string `json:"id"`
	RunID            string `json:"run_id"`
	EntryID          string `json:"entry_id"`
	ModelID          string `json:"model_id"`
	Model            string `json:"model"`
	MessageTokens    int    `json:"message_tokens"`
	PromptTokens     *int   `json:"prompt_tokens"`
	CompletionTokens *int   `json:"completion_tokens"`
	TotalTokens      *int   `json:"total_tokens"`
	CachedTokens     *int   `json:"cached_tokens"`
	PrefilledTokens  *int   `json:"prefilled_tokens"`
	ReasoningTokens  *int   `json:"reasoning_tokens"`
	ReasoningSource  string `json:"reasoning_source"`
	Drift            *int   `json:"drift"`
	Prediction       string `json:"prediction"`

	PredictedPromptTokens *int   `json:"predicted_prompt_tokens"`
	ContextWindow         *int   `json:"context_window"`
	Endpoint              string `json:"endpoint"`
	QueueMS               *int   `json:"queue_ms"`
	Slot                  *int   `json:"slot"`
	Priority              string `json:"priority"`
}

type callsWire struct {
	SessionID string          `json:"session_id"`
	Calls     []modelCallWire `json:"calls"`
}

type contextWire struct {
	agent.Context
	Sources       map[string]string `json:"sources"`
	DroppedEffort string            `json:"dropped_effort"`
	Call          *modelCallWire    `json:"call"`
	ContextWindow int               `json:"context_window"`
	Breakdown     *breakdownWire    `json:"breakdown"`
	Prediction    *struct {
		Tokens int    `json:"tokens"`
		Method string `json:"method"`
	} `json:"prediction"`
}

type breakdownWire struct {
	agent.Breakdown
	PromptTokens *int   `json:"prompt_tokens"`
	PromptSource string `json:"prompt_source"`
	Remainder    *int   `json:"remainder"`
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
