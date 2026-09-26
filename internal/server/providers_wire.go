package server

import (
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
)

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
