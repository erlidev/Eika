package server

import (
	"context"
	"encoding/json"

	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/store"
)

// effectiveWindow is the context a model really has: the smaller of the
// window configured on its row and the per-request context its endpoint
// reported (probed), which is zero when the endpoint stated none. Everything
// that judges a request against a window uses this number, so a configured
// window larger than what the server holds cannot silently truncate a prompt.
func effectiveWindow(configured, probed int) int {
	if probed > 0 && probed < configured {
		return probed
	}
	return configured
}

// probedWindow is the per-request context the last probe of p reported, zero
// when it stated none or no probe has succeeded.
func probedWindow(p store.Provider) int {
	var caps probe.Capabilities
	if err := json.Unmarshal(p.Capabilities, &caps); err != nil {
		return 0
	}
	return max(caps.Window, 0)
}

// window is the effective window of the run's model, zero when it has none.
func (c runConfig) window() int {
	return effectiveWindow(c.model.ContextWindow, c.probedWindow)
}

// withProbe returns c with the per-request context p, the provider the run's
// model is on, last reported, which its effective window needs.
func (c runConfig) withProbe(p store.Provider) runConfig {
	c.probedWindow = probedWindow(p)
	return c
}

// probed returns c with the probed window of its model's provider, read from
// the store; a configuration with no model is returned as it is.
func (s *Server) probed(ctx context.Context, c runConfig) (runConfig, error) {
	if !c.modelOK {
		return c, nil
	}
	p, err := s.deps.Store.Provider(ctx, c.model.ProviderID)
	if err != nil {
		return runConfig{}, err
	}
	return c.withProbe(p), nil
}

// modelBodyOf renders a model on the wire with its endpoint's probed window.
func (s *Server) modelBodyOf(ctx context.Context, m store.Model) (modelBody, error) {
	p, err := s.deps.Store.Provider(ctx, m.ProviderID)
	if err != nil {
		return modelBody{}, err
	}
	return asModel(m, probedWindow(p)), nil
}
