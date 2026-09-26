package server

import (
	"time"

	"github.com/erlidev/eika/internal/store"
)

// modelBody is one model on the wire.
type modelBody struct {
	ID               string    `json:"id"`
	ProviderID       string    `json:"provider_id"`
	Name             string    `json:"name"`
	Model            string    `json:"model"`
	ContextWindow    int       `json:"context_window"`
	MaxOutput        int       `json:"max_output"`
	ReasoningEffort  string    `json:"reasoning_effort,omitempty"`
	ReasoningEfforts []string  `json:"reasoning_efforts"`
	ThinkingSwitch   string    `json:"thinking_switch"`
	PreserveThinking bool      `json:"preserve_thinking"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// modelsResponse is the body of GET /api/models.
type modelsResponse struct {
	Models []modelBody `json:"models"`
	// Default is the model a run uses when the request names none: the
	// default_model setting when it names a model that exists, the first
	// model otherwise, and empty when there are no models.
	Default string `json:"default,omitempty"`
}

// createModelRequest is the body of POST /api/models.
type createModelRequest struct {
	ProviderID string `json:"provider_id"`
	// Name defaults to Model.
	Name             string   `json:"name"`
	Model            string   `json:"model"`
	ContextWindow    int      `json:"context_window"`
	MaxOutput        int      `json:"max_output"`
	ReasoningEffort  string   `json:"reasoning_effort"`
	ReasoningEfforts []string `json:"reasoning_efforts"`
	ThinkingSwitch   string   `json:"thinking_switch"`
	PreserveThinking bool     `json:"preserve_thinking"`
}

// updateModelRequest is the body of PATCH /api/models/{id}. An absent field
// is left alone; the provider does not change.
type updateModelRequest struct {
	Name             *string   `json:"name"`
	Model            *string   `json:"model"`
	ContextWindow    *int      `json:"context_window"`
	MaxOutput        *int      `json:"max_output"`
	ReasoningEffort  *string   `json:"reasoning_effort"`
	ReasoningEfforts *[]string `json:"reasoning_efforts"`
	ThinkingSwitch   *string   `json:"thinking_switch"`
	PreserveThinking *bool     `json:"preserve_thinking"`
}

// testModelRequest is the body of POST /api/models/test: a model on a stored
// provider, saved or not yet.
type testModelRequest struct {
	ProviderID       string `json:"provider_id"`
	Model            string `json:"model"`
	ReasoningEffort  string `json:"reasoning_effort"`
	ThinkingSwitch   string `json:"thinking_switch"`
	PreserveThinking bool   `json:"preserve_thinking"`
}

// testModelResponse is the body of POST /api/models/test.
type testModelResponse struct {
	// Reply is what the model answered. A reasoning model that spent the
	// whole budget thinking answers nothing, which still proves the model
	// is there.
	Reply      string `json:"reply"`
	StopReason string `json:"stop_reason"`
	LatencyMS  int64  `json:"latency_ms"`
}

// effortList is how a model's choices go on the wire: always an array, so a
// client never has to tell an absent list from an empty one.
func effortList(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// asModel renders a model on the wire.
func asModel(m store.Model) modelBody {
	return modelBody{
		ID:               m.ID,
		ProviderID:       m.ProviderID,
		Name:             m.Name,
		Model:            m.Model,
		ContextWindow:    m.ContextWindow,
		MaxOutput:        m.MaxOutput,
		ReasoningEffort:  m.ReasoningEffort,
		ReasoningEfforts: effortList(m.ReasoningEfforts),
		ThinkingSwitch:   m.ThinkingSwitch,
		PreserveThinking: m.PreserveThinking,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}
