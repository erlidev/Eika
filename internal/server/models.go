package server

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/erlidev/eika/internal/imaging"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
)

// testPrompt is the message a model test sends.
const testPrompt = "Reply with the single word: ready"

// testImage is the picture a model test for image input sends with its
// question: a 16 by 16 pixel teal square as a PNG, which every endpoint that
// reads images accepts.
var testImage = func() provider.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.NRGBA{G: 128, B: 128, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	// Encoding into memory cannot fail.
	_ = png.Encode(&buf, img)
	return provider.Image{MediaType: imaging.MediaPNG, Data: buf.Bytes(), Width: 16, Height: 16}
}()

// handleListModels lists every model and the one a run uses by default.
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.deps.Store.Models(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]modelBody, 0, len(models))
	for _, m := range models {
		out = append(out, asModel(m))
	}
	def, _ := s.defaultModel(r.Context(), models)
	writeJSON(w, s.log, http.StatusOK, modelsResponse{Models: out, Default: def.Name})
}

// handleCreateModel adds a model to a provider.
func (s *Server) handleCreateModel(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createModelRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m := store.Model{
		ProviderID:       req.ProviderID,
		Name:             strings.TrimSpace(req.Name),
		Model:            strings.TrimSpace(req.Model),
		ContextWindow:    req.ContextWindow,
		MaxOutput:        req.MaxOutput,
		ReasoningEffort:  req.ReasoningEffort,
		ReasoningEfforts: req.ReasoningEfforts,
		ThinkingSwitch:   req.ThinkingSwitch,
		PreserveThinking: req.PreserveThinking,
		ImageInput:       req.ImageInput,
	}
	if m.Name == "" {
		m.Name = m.Model
	}
	if err := validateModel(m); err != nil {
		s.fail(w, r, err)
		return
	}
	created, err := s.deps.Store.CreateModel(r.Context(), m)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			err = conflictf("a model named %q already exists; give this one another name", m.Name)
		case errors.Is(err, store.ErrNotFound):
			err = invalidf("provider %s does not exist", m.ProviderID)
		}
		s.fail(w, r, err)
		return
	}
	s.log.Info("model created", "model_id", created.ID, "name", created.Name, "provider_id", created.ProviderID)
	writeJSON(w, s.log, http.StatusCreated, asModel(created))
}

// handleUpdateModel changes a model. Renaming a model keeps it the default
// and keeps the utility tasks assigned to it.
func (s *Server) handleUpdateModel(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[updateModelRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, err := s.deps.Store.Model(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	oldName := m.Name
	if req.Name != nil {
		m.Name = strings.TrimSpace(*req.Name)
	}
	if req.Model != nil {
		m.Model = strings.TrimSpace(*req.Model)
	}
	if req.ContextWindow != nil {
		m.ContextWindow = *req.ContextWindow
	}
	if req.MaxOutput != nil {
		m.MaxOutput = *req.MaxOutput
	}
	if req.ReasoningEffort != nil {
		m.ReasoningEffort = *req.ReasoningEffort
	}
	if req.ReasoningEfforts != nil {
		m.ReasoningEfforts = *req.ReasoningEfforts
	}
	if req.ThinkingSwitch != nil {
		m.ThinkingSwitch = *req.ThinkingSwitch
	}
	if req.PreserveThinking != nil {
		m.PreserveThinking = *req.PreserveThinking
	}
	if req.ImageInput != nil {
		m.ImageInput = *req.ImageInput
	}
	if err := validateModel(m); err != nil {
		s.fail(w, r, err)
		return
	}
	updated, err := s.deps.Store.UpdateModel(r.Context(), m)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = conflictf("a model named %q already exists", m.Name)
		}
		s.fail(w, r, err)
		return
	}
	if updated.Name != oldName {
		if err := s.renameModelSettings(r.Context(), oldName, updated.Name); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.log.Info("model updated", "model_id", updated.ID, "name", updated.Name)
	writeJSON(w, s.log, http.StatusOK, asModel(updated))
}

// handleDeleteModel removes a model. When it was the default, the first
// remaining model becomes the default until the user picks another.
func (s *Server) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deps.Store.DeleteModel(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("model deleted", "model_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleTestModel sends one small request to a model and reports what came
// back, so the setup screens can prove a model name before a run depends on
// it.
func (s *Server) handleTestModel(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[testModelRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		s.fail(w, r, invalidf("model is required"))
		return
	}
	if !provider.ValidReasoningEffort(req.ReasoningEffort) {
		s.fail(w, r, invalidEffort(req.ReasoningEffort))
		return
	}
	if !provider.ValidThinkingSwitch(provider.ThinkingSwitch(req.ThinkingSwitch)) {
		s.fail(w, r, invalidSwitch(req.ThinkingSwitch))
		return
	}
	p, err := s.deps.Store.Provider(r.Context(), req.ProviderID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key, err := s.openKey(p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	client, err := s.deps.Providers.Build(p.Kind, provider.Endpoint{BaseURL: p.BaseURL, APIKey: key})
	if err != nil {
		s.fail(w, r, invalidf("%s", scrub(err.Error(), key)))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), testTimeout)
	defer cancel()
	sampling := provider.Sampling{MaxOutput: new(testMaxTokens)}
	if req.ReasoningEffort != "" {
		sampling.ReasoningEffort = &req.ReasoningEffort
	}
	question := provider.UserMessage(testPrompt)
	if req.ImageInput {
		question.Images = []provider.Image{testImage}
	}
	started := time.Now()
	reply, stop, err := provider.Complete(ctx, client, provider.Request{
		Model:            model,
		Messages:         []provider.Message{question},
		Sampling:         sampling,
		ThinkingSwitch:   provider.ThinkingSwitch(req.ThinkingSwitch),
		PreserveThinking: req.PreserveThinking,
	})
	if err != nil {
		s.fail(w, r, endpointFailure(p.BaseURL, err, key))
		return
	}
	writeJSON(w, s.log, http.StatusOK, testModelResponse{
		Reply:      strings.TrimSpace(reply),
		StopReason: stop,
		LatencyMS:  time.Since(started).Milliseconds(),
	})
}

// defaultModel returns the model a run uses when it names none: the one the
// settings name, or the first. It reports false when there are no models.
func (s *Server) defaultModel(ctx context.Context, models []store.Model) (store.Model, bool) {
	if len(models) == 0 {
		return store.Model{}, false
	}
	if name := s.defaultModelSetting(ctx); name != "" {
		for _, m := range models {
			if m.Name == name {
				return m, true
			}
		}
		s.log.Warn("default model setting names no model", "model", name)
	}
	return models[0], true
}

// validateModel rejects a model a run could not use.
func validateModel(m store.Model) error {
	switch {
	case m.Model == "" || len(m.Model) > maxModelID:
		return invalidf("model must be the endpoint's identifier for it, 1 to %d characters", maxModelID)
	case len(m.Name) > maxModelName || strings.IndexFunc(m.Name, unicode.IsControl) >= 0:
		return invalidf("name must be at most %d characters", maxModelName)
	case m.ContextWindow < 1 || m.ContextWindow > maxContextWindow:
		return invalidf("context_window must be a positive number of tokens")
	case m.MaxOutput < 1 || m.MaxOutput > m.ContextWindow:
		return invalidf("max_output must be a positive number of tokens no larger than context_window")
	case !provider.ValidReasoningEffort(m.ReasoningEffort):
		return invalidEffort(m.ReasoningEffort)
	case !provider.ValidThinkingSwitch(provider.ThinkingSwitch(m.ThinkingSwitch)):
		return invalidSwitch(m.ThinkingSwitch)
	case len(m.ReasoningEfforts) > maxReasoningEfforts:
		return invalidf("reasoning_efforts holds at most %d values", maxReasoningEfforts)
	}
	seen := make(map[string]struct{}, len(m.ReasoningEfforts))
	for _, effort := range m.ReasoningEfforts {
		if effort == "" {
			return invalidf("reasoning_efforts holds no empty value; the endpoint default is not one of the choices")
		}
		if !provider.ValidReasoningEffort(effort) {
			return invalidEffort(effort)
		}
		if _, exists := seen[effort]; exists {
			return invalidf("reasoning_efforts contains duplicate value %q", effort)
		}
		seen[effort] = struct{}{}
	}
	return nil
}

// invalidEffort reports a reasoning_effort the harness will not send. The
// vocabulary belongs to the endpoint, so the message describes the shape.
func invalidEffort(effort string) error {
	return invalidf("reasoning_effort %q must be at most %d letters, digits, hyphens, or underscores",
		effort, provider.MaxReasoningEffortLen)
}

// invalidSwitch reports a thinking_switch the harness cannot send.
func invalidSwitch(s string) error {
	return invalidf("thinking_switch %q must be %s, %s, or %s", s,
		provider.SwitchReasoningEffort, provider.SwitchTemplate, provider.SwitchThinking)
}
