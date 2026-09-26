package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/store"
)

// defaultProviderKind is the kind a provider request that names none gets.
// Every OpenAI-compatible endpoint is this kind.
const defaultProviderKind = "openai"

// Bounds on what the provider and model routes accept and wait for.
const (
	maxProviderName = 64
	maxModelName    = 128
	maxModelID      = 256
	maxAPIKey       = 4096
	// maxReasoningEfforts bounds the choices one model may offer; a list
	// longer than this is not something a user cycles through.
	maxReasoningEfforts = 12
	// maxContextWindow is far above any model today; it only catches a typo
	// with too many zeros.
	maxContextWindow = 100_000_000
	// probeTimeout bounds asking an endpoint which models it serves.
	probeTimeout = 20 * time.Second
	// testTimeout bounds the one small request a model test sends. A
	// reasoning model can think for a while before it answers.
	testTimeout = 90 * time.Second
	// testMaxTokens leaves a reasoning model room to think and still say a
	// word, at a cost too small to matter.
	testMaxTokens = 512
	// keyHintLength is how many trailing characters of a key the UI may see,
	// so the user can tell which key is stored.
	keyHintLength = 4
	// minHintedKey is the shortest key a hint is shown for: on a shorter one
	// four characters give too much of it away.
	minHintedKey = 16
)

// testPrompt is the message a model test sends.
const testPrompt = "Reply with the single word: ready"

// providerBody is one provider on the wire. The key itself never leaves the
// harness.
type providerBody struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	BaseURL   string `json:"base_url"`
	APIKeySet bool   `json:"api_key_set"`
	// APIKeyHint is the last characters of a long key, so the user can tell
	// which key is stored.
	APIKeyHint string    `json:"api_key_hint,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// providersResponse is the body of GET /api/providers.
type providersResponse struct {
	Providers []providerBody `json:"providers"`
	// Kinds are the provider kinds this harness can talk to.
	Kinds []string `json:"kinds"`
}

// createProviderRequest is the body of POST /api/providers.
type createProviderRequest struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

// updateProviderRequest is the body of PATCH /api/providers/{id}. An absent
// field is left alone.
type updateProviderRequest struct {
	Name    *string `json:"name"`
	BaseURL *string `json:"base_url"`
	// APIKey replaces the stored key; the empty string removes it.
	APIKey *string `json:"api_key"`
}

// probeRequest is the body of POST /api/providers/probe. With a provider id
// the stored provider is probed, and any other field given overrides what is
// stored, so a form can check a new key before it saves it. Without one, the
// fields describe an endpoint nothing has saved yet.
type probeRequest struct {
	ProviderID string  `json:"provider_id"`
	Kind       string  `json:"kind"`
	BaseURL    *string `json:"base_url"`
	APIKey     *string `json:"api_key"`
}

// probeResponse is the body of POST /api/providers/probe.
type probeResponse struct {
	Models []provider.ModelInfo `json:"models"`
}

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

// handleListProviders lists every provider and the kinds a new one may have.
func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.Store.Providers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]providerBody, 0, len(rows))
	for _, p := range rows {
		out = append(out, s.asProvider(p))
	}
	writeJSON(w, s.log, http.StatusOK, providersResponse{Providers: out, Kinds: s.deps.Providers.Kinds()})
}

// handleCreateProvider records a provider with its sealed key.
func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createProviderRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := store.Provider{
		Name:    strings.TrimSpace(req.Name),
		Kind:    strings.TrimSpace(req.Kind),
		BaseURL: strings.TrimSpace(req.BaseURL),
	}
	if p.Kind == "" {
		p.Kind = defaultProviderKind
	}
	if err := s.validateProvider(p); err != nil {
		s.fail(w, r, err)
		return
	}
	if p.APIKey, err = s.sealKey(req.APIKey); err != nil {
		s.fail(w, r, err)
		return
	}
	created, err := s.deps.Store.CreateProvider(r.Context(), p)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = conflictf("a provider named %q already exists", p.Name)
		}
		s.fail(w, r, err)
		return
	}
	s.log.Info("provider created", "provider_id", created.ID, "name", created.Name, "kind", created.Kind)
	writeJSON(w, s.log, http.StatusCreated, s.asProvider(created))
}

// handleUpdateProvider changes a provider's name, endpoint, or key. A new
// base URL without a new key clears the stored key, so that it is never sent
// to an endpoint it was not entered for.
func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[updateProviderRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.deps.Store.Provider(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if req.Name != nil {
		p.Name = strings.TrimSpace(*req.Name)
	}
	keyCleared := false
	if req.BaseURL != nil {
		baseURL := strings.TrimSpace(*req.BaseURL)
		// A key belongs to the endpoint it was entered for; it is never
		// sent to another one the user did not give it to.
		if baseURL != p.BaseURL && req.APIKey == nil && len(p.APIKey) > 0 {
			p.APIKey, keyCleared = nil, true
		}
		p.BaseURL = baseURL
	}
	if err := s.validateProvider(p); err != nil {
		s.fail(w, r, err)
		return
	}
	if req.APIKey != nil {
		if p.APIKey, err = s.sealKey(*req.APIKey); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	updated, err := s.deps.Store.UpdateProvider(r.Context(), p)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = conflictf("a provider named %q already exists", p.Name)
		}
		s.fail(w, r, err)
		return
	}
	s.log.Info("provider updated", "provider_id", updated.ID, "key_changed", req.APIKey != nil, "key_cleared", keyCleared)
	writeJSON(w, s.log, http.StatusOK, s.asProvider(updated))
}

// handleDeleteProvider removes a provider and its models. A run already
// going keeps the client it built; the next one cannot pick these models.
func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deps.Store.DeleteProvider(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("provider deleted", "provider_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleProbeProvider asks an endpoint which models it serves, which is both
// the connection test of the setup screens and where their model list comes
// from.
func (s *Server) handleProbeProvider(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[probeRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, key, err := s.probeTarget(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	client, err := s.deps.Providers.Build(p.Kind, provider.Endpoint{BaseURL: p.BaseURL, APIKey: key})
	if err != nil {
		s.fail(w, r, invalidf("%s", scrub(err.Error(), key)))
		return
	}
	lister, ok := client.(provider.Lister)
	if !ok {
		s.fail(w, r, invalidf("a %s provider cannot list its models; add them by name", p.Kind))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	models, err := lister.Models(ctx)
	if err != nil {
		s.fail(w, r, endpointFailure(p.BaseURL, err, key))
		return
	}
	if models == nil {
		models = []provider.ModelInfo{}
	}
	writeJSON(w, s.log, http.StatusOK, probeResponse{Models: models})
}

// probeTarget merges a probe request with the provider it names, if any, and
// returns the endpoint to probe with its key in the clear. A stored key is
// used only with the stored base URL.
func (s *Server) probeTarget(ctx context.Context, req probeRequest) (store.Provider, string, error) {
	var (
		p   store.Provider
		key string
	)
	if req.ProviderID != "" {
		stored, err := s.deps.Store.Provider(ctx, req.ProviderID)
		if err != nil {
			return store.Provider{}, "", err
		}
		if key, err = s.openKey(stored); err != nil {
			return store.Provider{}, "", err
		}
		p = stored
	} else {
		p.Kind = strings.TrimSpace(req.Kind)
		if p.Kind == "" {
			p.Kind = defaultProviderKind
		}
		// The name is not probed; it only has to pass validation.
		p.Name = "probe"
	}
	if req.BaseURL != nil {
		baseURL := strings.TrimSpace(*req.BaseURL)
		// The stored key goes only to the endpoint it was entered for.
		if req.ProviderID != "" && baseURL != p.BaseURL {
			key = ""
		}
		p.BaseURL = baseURL
	}
	if req.APIKey != nil {
		key = strings.TrimSpace(*req.APIKey)
	}
	if err := s.validateProvider(p); err != nil {
		return store.Provider{}, "", err
	}
	return p, key, nil
}

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
	started := time.Now()
	reply, stop, err := provider.Complete(ctx, client, provider.Request{
		Model:            model,
		Messages:         []provider.Message{provider.UserMessage(testPrompt)},
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

// providerFor builds a provider on the endpoint of the model a run's
// configuration resolved to. A deployment with no model has nothing to run
// on.
func (s *Server) providerFor(ctx context.Context, cfg runConfig) (provider.Provider, error) {
	if !cfg.modelOK {
		return nil, conflictf("no model is configured; add one under Settings, Models")
	}
	return s.providerOf(ctx, cfg.model)
}

// providerOf builds a provider on the endpoint of model m.
func (s *Server) providerOf(ctx context.Context, m store.Model) (provider.Provider, error) {
	p, err := s.deps.Store.Provider(ctx, m.ProviderID)
	if err != nil {
		return nil, err
	}
	key, err := s.openKey(p)
	if err != nil {
		return nil, err
	}
	client, err := s.deps.Providers.Build(p.Kind, provider.Endpoint{BaseURL: p.BaseURL, APIKey: key})
	if err != nil {
		return nil, fmt.Errorf("build provider %s: %s", p.Name, scrub(err.Error(), key))
	}
	return client, nil
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

// validateProvider rejects a provider the harness could not build a client
// for.
func (s *Server) validateProvider(p store.Provider) error {
	if p.Name == "" || len(p.Name) > maxProviderName || strings.IndexFunc(p.Name, unicode.IsControl) >= 0 {
		return invalidf("name must be 1 to %d characters", maxProviderName)
	}
	known := false
	for _, kind := range s.deps.Providers.Kinds() {
		known = known || kind == p.Kind
	}
	if !known {
		return invalidf("kind must be one of %s", strings.Join(s.deps.Providers.Kinds(), ", "))
	}
	return validateBaseURL(p.BaseURL)
}

// validateBaseURL accepts an http or https API root. Credentials, a query,
// and a fragment are refused: the key has a field of its own, and the client
// appends paths to this URL.
func validateBaseURL(raw string) error {
	if raw == "" {
		return invalidf("base_url is required, such as https://api.openai.com/v1")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalidf("base_url must be an http or https URL, such as https://api.openai.com/v1")
	}
	if u.User != nil {
		return invalidf("base_url must not contain credentials; put the key in api_key")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return invalidf("base_url must not contain a query string or fragment")
	}
	return nil
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

// sealKey trims and seals an API key for a provider row.
func (s *Server) sealKey(key string) ([]byte, error) {
	key = strings.TrimSpace(key)
	if len(key) > maxAPIKey {
		return nil, invalidf("api_key must be at most %d bytes", maxAPIKey)
	}
	return s.deps.Secrets.Seal(key)
}

// openKey returns a provider's key in the clear. A key the harness cannot
// open was sealed under a key file it no longer has, which the user fixes by
// entering the key again.
func (s *Server) openKey(p store.Provider) (string, error) {
	key, err := s.deps.Secrets.Open(p.APIKey)
	if errors.Is(err, secret.ErrCorrupt) {
		s.log.Error("open provider key", "provider_id", p.ID, "error", err)
		return "", conflictf("the API key of provider %s cannot be read, which happens when the harness's secret key file changes; enter the key again under Settings, Models", p.Name)
	}
	return key, err
}

// asProvider renders a provider on the wire with a hint of its key.
func (s *Server) asProvider(p store.Provider) providerBody {
	body := providerBody{
		ID:        p.ID,
		Name:      p.Name,
		Kind:      p.Kind,
		BaseURL:   p.BaseURL,
		APIKeySet: len(p.APIKey) > 0,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
	if key, err := s.deps.Secrets.Open(p.APIKey); err == nil && len(key) >= minHintedKey {
		body.APIKeyHint = key[len(key)-keyHintLength:]
	}
	return body
}

// asModel renders a model on the wire.
// effortList is how a model's choices go on the wire: always an array, so a
// client never has to tell an absent list from an empty one.
func effortList(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

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

// endpointFailure reports what an endpoint said as a request the user can
// fix, without the key, and with a hint for the one mistake almost everyone
// running a local model makes inside Docker.
func endpointFailure(baseURL string, err error, key string) error {
	message := scrub(err.Error(), key)
	var perr *provider.Error
	transport := !errors.As(err, &perr) || perr.StatusCode == 0
	if transport && loopback(baseURL) {
		message += "; the harness runs in a container, where localhost is the container itself: use http://host.docker.internal instead to reach the Docker host"
	}
	return invalidf("the endpoint could not be used: %s", message)
}

// loopback reports whether a URL names the local machine.
func loopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// scrub removes a secret from a message that may quote it.
func scrub(message, secret string) string {
	if secret == "" {
		return message
	}
	return strings.ReplaceAll(message, secret, "[REDACTED]")
}

// jsonString encodes a string as a JSON value.
func jsonString(v string) []byte {
	return fmt.Appendf(nil, "%q", v)
}
