package server

import (
	"encoding/json"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe"
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
	APIKeyHint string `json:"api_key_hint,omitempty"`
	// Backend, Pool, Slots, KV and KVTokens are the value in force for each:
	// what the last probe found, or what the user set, which Overridden names.
	Backend string `json:"backend"`
	// Pool is the hardware group; the provider's name unless the user set one.
	Pool     string `json:"pool"`
	Slots    int    `json:"slots"`
	KV       string `json:"kv"`
	KVTokens int    `json:"kv_tokens,omitempty"`
	// API is the wire protocol spoken to the endpoint, chat or responses:
	// the Responses check's choice, or the user's own. Its outcome is in
	// capabilities.responses.
	API string `json:"api"`
	// Capabilities are what the last successful probe found beyond those.
	Capabilities probe.Capabilities `json:"capabilities"`
	// Overridden names the fields the user set, which a probe leaves alone:
	// any of backend, pool, slots, kv and api.
	Overridden []string `json:"overridden"`
	// ProbedAt is when a probe last succeeded; absent if none has.
	ProbedAt *time.Time `json:"probed_at,omitempty"`
	// ProbeError is why the latest probe failed; absent after a success.
	ProbeError string    `json:"probe_error,omitempty"`
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
	// Backend, Pool, Slots, KV and API set what a probe would otherwise find;
	// a probe leaves a field set here alone. Pool "" means the provider's
	// name. KVTokens is the size of a shared cache and is set with KV.
	Backend  *string `json:"backend"`
	Pool     *string `json:"pool"`
	Slots    *int    `json:"slots"`
	KV       *string `json:"kv"`
	KVTokens *int    `json:"kv_tokens"`
	API      *string `json:"api"`
	// Reset hands fields (backend, pool, slots, kv, api) back to the probe.
	// Their values stay until the next probe.
	Reset []string `json:"reset"`
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
	// Probe is what the endpoint said about itself, before any override.
	Probe probeBody `json:"probe"`
	// Provider is the stored provider after the findings were recorded on
	// it. It is absent for an endpoint nothing stores.
	Provider *providerBody `json:"provider,omitempty"`
}

// probeBody is one probe's findings.
type probeBody struct {
	Backend  string `json:"backend"`
	Slots    int    `json:"slots"`
	KV       string `json:"kv"`
	KVTokens int    `json:"kv_tokens,omitempty"`
	// API is the protocol the Responses check chose; chat for an endpoint
	// nothing is stored for, which has no model of the user's to check with.
	API          string             `json:"api"`
	Capabilities probe.Capabilities `json:"capabilities"`
}

// asProbe renders a probe's findings on the wire, with the protocol its
// Responses check chose.
func asProbe(r probe.Result, api provider.API) probeBody {
	return probeBody{
		Backend:      string(r.Backend),
		Slots:        r.Slots,
		KV:           string(r.KV),
		KVTokens:     r.KVTokens,
		API:          string(api),
		Capabilities: r.Capabilities,
	}
}

// asProvider renders a provider on the wire with a hint of its key.
func (s *Server) asProvider(p store.Provider) providerBody {
	body := providerBody{
		ID:           p.ID,
		Name:         p.Name,
		Kind:         p.Kind,
		BaseURL:      p.BaseURL,
		APIKeySet:    len(p.APIKey) > 0,
		Backend:      p.Backend,
		Pool:         p.PoolName(),
		Slots:        p.Slots,
		KV:           p.KV,
		KVTokens:     p.KVTokens,
		API:          p.API,
		Capabilities: probe.Capabilities{Counting: probe.CountNone, CacheMetrics: probe.CacheUnknown},
		Overridden:   append([]string{}, p.Overridden...),
		ProbedAt:     p.ProbedAt,
		ProbeError:   p.ProbeError,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
	if p.ProbedAt != nil {
		// A row the harness wrote decodes; one it cannot read keeps the
		// defaults above rather than failing the whole list.
		_ = json.Unmarshal(p.Capabilities, &body.Capabilities)
	}
	if p.Pool != "" {
		body.Overridden = append(body.Overridden, store.OverridePool)
	}
	if key, err := s.deps.Secrets.Open(p.APIKey); err == nil && len(key) >= minHintedKey {
		body.APIKeyHint = key[len(key)-keyHintLength:]
	}
	return body
}
