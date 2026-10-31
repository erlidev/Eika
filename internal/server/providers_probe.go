package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/store"
)

// probeRunTimeout bounds what a probe asks an endpoint after its models are
// listed: a handful of small requests, each bounded on its own.
const probeRunTimeout = 30 * time.Second

// probeEndpoint lists the models an endpoint serves, which is also the
// connection test, and probes what kind of server it is. The error is the one
// a user can act on, with the key removed.
func (s *Server) probeEndpoint(ctx context.Context, p store.Provider, key string) ([]provider.ModelInfo, probe.Result, error) {
	client, err := s.deps.Providers.Build(p.Kind, provider.Endpoint{BaseURL: p.BaseURL, APIKey: key})
	if err != nil {
		return nil, probe.Result{}, invalidf("%s", scrub(err.Error(), key))
	}
	lister, ok := client.(provider.Lister)
	if !ok {
		return nil, probe.Result{}, invalidf("a %s provider cannot list its models; add them by name", p.Kind)
	}
	listCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	models, err := lister.Models(listCtx)
	if err != nil {
		return nil, probe.Result{}, endpointFailure(p.BaseURL, err, key)
	}
	if models == nil {
		models = []provider.ModelInfo{}
	}
	runCtx, cancelRun := context.WithTimeout(ctx, probeRunTimeout)
	defer cancelRun()
	return models, probe.Run(runCtx, probe.Target{BaseURL: p.BaseURL, APIKey: key, Models: models}), nil
}

// recordProbe stores a probe's findings on a stored provider, with the
// protocol its Responses check chose. Fields the user overrode keep their
// values.
func (s *Server) recordProbe(ctx context.Context, id string, res probe.Result, api provider.API) (store.Provider, error) {
	capabilities, err := json.Marshal(res.Capabilities)
	if err != nil {
		return store.Provider{}, fmt.Errorf("encode capabilities: %w", err)
	}
	return s.deps.Store.RecordProbe(ctx, id, store.ProbeResult{
		Backend:      string(res.Backend),
		Slots:        res.Slots,
		KV:           string(res.KV),
		KVTokens:     res.KVTokens,
		API:          string(api),
		Capabilities: capabilities,
		At:           time.Now().UTC(),
	})
}

// probeStored probes one stored provider and records the outcome: the
// findings, or why there are none. A failure leaves every earlier result. It
// asks the model only if the Responses check is out of date (see
// withConformance); the explicit probe route always does.
func (s *Server) probeStored(ctx context.Context, p store.Provider) error {
	key, err := s.openKey(p)
	if err == nil {
		var res probe.Result
		if _, res, err = s.probeEndpoint(ctx, p, key); err == nil {
			var api provider.API
			res, api = s.withConformance(ctx, p, key, res, false)
			_, err = s.recordProbe(ctx, p.ID, res, api)
			return err
		}
	}
	s.recordProbeFailure(ctx, p.ID, err)
	return err
}

// probeProviders probes every stored provider, side by side, and returns when
// all are done. It runs at every start. A provider that cannot be reached
// is logged and keeps what an earlier probe found; Core serves meanwhile.
func (s *Server) probeProviders(ctx context.Context) {
	rows, err := s.deps.Store.Providers(ctx)
	if err != nil {
		s.log.Error("list providers to probe", "error", err)
		return
	}
	var wg sync.WaitGroup
	for _, p := range rows {
		wg.Go(func() {
			if err := s.probeStored(ctx, p); err != nil {
				if ctx.Err() == nil {
					s.log.Warn("provider probe failed", "provider_id", p.ID, "name", p.Name, "error", err)
				}
				return
			}
			s.log.Info("provider probed", "provider_id", p.ID, "name", p.Name)
		})
	}
	wg.Wait()
}
