package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/erlidev/eika/internal/store"
)

// settingDefaultModel is the settings key naming the model a run uses when the
// request does not name one.
const settingDefaultModel = "default_model"

// settingsResponse is the body of GET and PUT /api/settings: the settings
// table as one JSON object.
type settingsResponse struct {
	Settings map[string]json.RawMessage `json:"settings"`
}

// modelBody is one configured model on the wire.
type modelBody struct {
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window"`
	MaxOutput     int    `json:"max_output"`
}

// modelsResponse is the body of GET /api/models.
type modelsResponse struct {
	Models []modelBody `json:"models"`
	// Default is the model a run uses when the request names none.
	Default string `json:"default,omitempty"`
}

// handleSettings returns every setting.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, settingsResponse{Settings: settings})
}

// handlePutSettings writes the keys the body carries, leaving the others
// alone, and returns the whole table as it now stands.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[map[string]json.RawMessage](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for key, value := range req {
		if key == "" {
			s.fail(w, r, invalidf("a setting key is empty"))
			return
		}
		if err := s.deps.Store.SetSetting(r.Context(), key, value); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	settings, err := s.settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("settings written", "keys", len(req))
	writeJSON(w, s.log, http.StatusOK, settingsResponse{Settings: settings})
}

// handleModels lists the models the deployment configured.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	out := make([]modelBody, 0, len(s.cfg.Models))
	for _, name := range s.deps.Models.Names() {
		body := modelBody{Name: name}
		if m, ok := s.cfg.Model(name); ok {
			body.ContextWindow, body.MaxOutput = m.ContextWindow, m.MaxOutput
		}
		out = append(out, body)
	}
	writeJSON(w, s.log, http.StatusOK, modelsResponse{Models: out, Default: s.defaultModel(r.Context())})
}

// settings reads the settings table as one object.
func (s *Server) settings(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.deps.Store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

// defaultModel returns the model named by the settings, or an empty string
// when the user has not chosen one.
func (s *Server) defaultModel(ctx context.Context) string {
	if s.deps.Store == nil {
		return ""
	}
	setting, err := s.deps.Store.Setting(ctx, settingDefaultModel)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("read default model setting", "error", err)
		}
		return ""
	}
	var name string
	if err := json.Unmarshal(setting.Value, &name); err != nil {
		s.log.Warn("default model setting is not a string", "value", string(setting.Value))
		return ""
	}
	return name
}
