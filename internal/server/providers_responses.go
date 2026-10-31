package server

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/store"
)

// conformanceTimeout bounds the whole Responses conformance check: two
// requests to the model, each bounded on its own, and a count between them.
const conformanceTimeout = 4 * time.Minute

// providerEndpoint is the endpoint of a stored provider as a client is built
// on it: the protocol the row names, and whether it is a hosted API, which
// decides what a request to it may carry.
func providerEndpoint(p store.Provider, key string) provider.Endpoint {
	return provider.Endpoint{
		BaseURL: p.BaseURL,
		APIKey:  key,
		API:     provider.API(p.API),
		Hosted:  hostedEndpoint(p),
	}
}

// hostedEndpoint reports whether the provider is a hosted API rather than a
// server on the user's own network, as its backend says.
func hostedEndpoint(p store.Provider) bool {
	return probe.Backend(p.Backend) == probe.OpenAI
}

// apiOf is the protocol a probe's verdict chooses: the Responses API only if
// the check passed.
func apiOf(c *probe.Conformance) provider.API {
	if c != nil && c.Verdict == probe.Passed {
		return provider.APIResponses
	}
	return provider.APIChat
}

// withConformance returns res with the outcome of the Responses conformance
// check in its capabilities, and the protocol that outcome chooses. The check
// sends real requests to the user's model, so it runs only when it has to:
// when asked for (force), when it never ran, or when the server's build or
// the model it would ask is not the one it last ran against. Otherwise the
// last outcome stands, so a start of Core sends the model nothing.
func (s *Server) withConformance(ctx context.Context, p store.Provider, key string, res probe.Result, force bool) (probe.Result, provider.API) {
	var prior probe.Capabilities
	if p.ProbedAt != nil {
		_ = json.Unmarshal(p.Capabilities, &prior)
	}
	model, known := s.checkModel(ctx, p.ID)
	if !force && prior.Responses != nil && prior.Responses.Current(res.Capabilities.Build, model.Model) {
		res.Capabilities.Responses = prior.Responses
		return res, apiOf(prior.Responses)
	}
	conf := s.checkResponses(ctx, p, key, res, model, known)
	res.Capabilities.Responses = &conf
	s.log.Info("responses conformance checked", "provider_id", p.ID, "name", p.Name, "model", model.Model,
		"verdict", conf.Verdict, "reason", conf.Reason, "reasoning", conf.Reasoning, "cache_metrics", conf.CacheMetrics,
		"counted", conf.Counted, "drift", conf.Drift)
	return res, apiOf(&conf)
}

// checkModel is the model the Responses check asks on a provider: the oldest
// chat model the user configured on it. A decision model is never asked, since
// it cannot answer a chat request. It reports false when there is none, in
// which case nobody knows which model the user means.
func (s *Server) checkModel(ctx context.Context, providerID string) (store.Model, bool) {
	models, err := s.deps.Store.Models(ctx)
	if err != nil {
		s.log.Warn("list models for the responses check", "provider_id", providerID, "error", err)
		return store.Model{}, false
	}
	for _, m := range chatModels(models) {
		if m.ProviderID == providerID {
			return m, true
		}
	}
	return store.Model{}, false
}

// checkResponses runs the Responses conformance check against the provider's
// endpoint with its model, counting the second request with a counter of its
// own where the endpoint counts exactly. A provider with no model is skipped.
func (s *Server) checkResponses(ctx context.Context, p store.Provider, key string, res probe.Result, model store.Model, known bool) probe.Conformance {
	now := time.Now().UTC()
	if !known {
		return probe.Conformance{
			Verdict: probe.Skipped, Build: res.Capabilities.Build, CheckedAt: now,
			Reason: "no chat model is configured on this provider, so there is no model to ask",
		}
	}
	// The row as the check will use it: the backend the probe found unless the
	// user set another, and the Responses API.
	row := p
	row.API = string(provider.APIResponses)
	if !slices.Contains(p.Overridden, store.OverrideBackend) {
		row.Backend = string(res.Backend)
	}
	client, err := s.deps.Providers.Build(row.Kind, providerEndpoint(row, key))
	if err != nil {
		return probe.Conformance{
			Verdict: probe.Failed, Model: model.Model, Build: res.Capabilities.Build, CheckedAt: now,
			Reason: scrub(err.Error(), key),
		}
	}
	check := probe.ResponsesCheck{
		Provider: client, Model: model.Model, Build: res.Capabilities.Build,
		// A hosted API is asked for encrypted reasoning only for a model the
		// user keeps the thinking of; a server of the user's own takes the
		// reasoning back whatever the setting, and the check should see it do so.
		PreserveThinking: !providerEndpoint(row, key).Hosted || model.PreserveThinking,
	}
	if res.Capabilities.Counting == probe.CountChat {
		encoder, _ := client.(provider.ChatEncoder)
		ctr := counter.New(counter.NewRoutes(counter.Config{
			BaseURL: p.BaseURL, APIKey: key, Model: model.Model,
			Backend: res.Backend, Counting: res.Capabilities.Counting,
		}, encoder))
		check.Predict = func(ctx context.Context, req provider.Request) (provider.Prediction, error) {
			return ctr.Peek(ctx, req), nil
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, conformanceTimeout)
	defer cancel()
	conf := probe.CheckResponses(checkCtx, check)
	conf.Reason = scrub(conf.Reason, key)
	return conf
}
