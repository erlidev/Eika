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
