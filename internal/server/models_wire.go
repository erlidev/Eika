package server

import (
	"time"

	"github.com/erlidev/eika/internal/store"
)

// modelBody is one model on the wire.
type modelBody struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	// Kind is chat, or decision for a model that answers typed questions and
	// cannot hold a conversation.
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Model         string `json:"model"`
	ContextWindow int    `json:"context_window"`
	// ProbedWindow is the context one request may use on the model's endpoint,
	// as its last probe read it, and absent when the endpoint states none.
	ProbedWindow int `json:"probed_window,omitempty"`
	// EffectiveWindow is the window runs on the model are judged against: the
	// smaller of ContextWindow and ProbedWindow. Less than ContextWindow means
	// the endpoint holds less than the configured window allows.
	EffectiveWindow  int       `json:"effective_window"`
	MaxOutput        int       `json:"max_output"`
	ReasoningEffort  string    `json:"reasoning_effort,omitempty"`
	ReasoningEfforts []string  `json:"reasoning_efforts"`
	ThinkingSwitch   string    `json:"thinking_switch"`
	PreserveThinking bool      `json:"preserve_thinking"`
	ImageInput       bool      `json:"image_input"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// modelsResponse is the body of GET /api/models.
type modelsResponse struct {
	Models []modelBody `json:"models"`
	// Default is the model a run uses when the request names none: the
	// default_model setting when it names a chat model that exists, the first
	// chat model otherwise, and empty when there are no chat models.
	Default string `json:"default,omitempty"`
}

// createModelRequest is the body of POST /api/models.
type createModelRequest struct {
	ProviderID string `json:"provider_id"`
	// Kind defaults to chat. It cannot be changed afterwards.
	Kind string `json:"kind"`
	// Name defaults to Model.
	Name             string   `json:"name"`
	Model            string   `json:"model"`
	ContextWindow    int      `json:"context_window"`
	MaxOutput        int      `json:"max_output"`
	ReasoningEffort  string   `json:"reasoning_effort"`
	ReasoningEfforts []string `json:"reasoning_efforts"`
	ThinkingSwitch   string   `json:"thinking_switch"`
	PreserveThinking bool     `json:"preserve_thinking"`
	ImageInput       bool     `json:"image_input"`
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
	ImageInput       *bool     `json:"image_input"`
}

// testModelRequest is the body of POST /api/models/test: a model on a stored
// provider, saved or not yet.
type testModelRequest struct {
	ProviderID string `json:"provider_id"`
	// Kind defaults to chat. A decision model is not tested (handleTestModel).
	Kind             string `json:"kind"`
	Model            string `json:"model"`
	ReasoningEffort  string `json:"reasoning_effort"`
	ThinkingSwitch   string `json:"thinking_switch"`
	PreserveThinking bool   `json:"preserve_thinking"`
	// ImageInput sends a small picture with the question, which proves the
	// endpoint takes images for the model.
	ImageInput bool `json:"image_input"`
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

// asModel renders a model on the wire, with probed the per-request context
// its endpoint reported, zero when it stated none.
func asModel(m store.Model, probed int) modelBody {
	return modelBody{
		ID:               m.ID,
		ProviderID:       m.ProviderID,
		Kind:             m.Kind,
		Name:             m.Name,
		Model:            m.Model,
		ContextWindow:    m.ContextWindow,
		ProbedWindow:     probed,
		EffectiveWindow:  effectiveWindow(m.ContextWindow, probed),
		MaxOutput:        m.MaxOutput,
		ReasoningEffort:  m.ReasoningEffort,
		ReasoningEfforts: effortList(m.ReasoningEfforts),
		ThinkingSwitch:   m.ThinkingSwitch,
		PreserveThinking: m.PreserveThinking,
		ImageInput:       m.ImageInput,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}
