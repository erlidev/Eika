package server

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/store"
)

// slotCheckTimeout bounds the whole slot affinity check: three small requests,
// each bounded on its own.
const slotCheckTimeout = 4 * time.Minute

// takesSlotHints reports whether requests to the provider name the server slot
// they run on: a llama.cpp server with more than one slot on which the slot
// affinity check found that a named slot keeps its prefix. It is the one rule
// behind both the queue, which places a session's calls by slot, and the
// client, which sends the number, so no other server is sent id_slot, and a
// server that would only lose the prefix of a session it pinned is not pinned
// to. It reads the backend and slot count in force, so a user who set either
// decides it; an endpoint the check has not passed gets none.
func takesSlotHints(p store.Provider) bool {
	if !namesSlots(probe.Backend(p.Backend), p.Slots) {
		return false
	}
	var caps probe.Capabilities
	if err := json.Unmarshal(p.Capabilities, &caps); err != nil {
		return false
	}
	return caps.SlotAffinity != nil && caps.SlotAffinity.Verdict == probe.Passed
}

// namesSlots reports whether requests to a server of this kind and size could
// name a slot: llama.cpp with several. Whether naming one helps is the check's
// verdict.
func namesSlots(backend probe.Backend, slots int) bool {
	return backend == probe.LlamaCpp && slots > 1
}

// withSlotAffinity returns res with the outcome of the slot affinity check in
// its capabilities, for a llama.cpp server with several slots. Like the
// Responses check it sends real requests to the user's model, so it runs only
// when asked for (force), when it never ran, or when the server's build or the
// model it would ask is not the one it last ran against; a start of Core
// otherwise sends the model nothing. A server whose requests cannot name a slot
// has no outcome. api is the protocol the provider will use, which the check
// uses as well, since the two routes need not behave alike.
func (s *Server) withSlotAffinity(ctx context.Context, p store.Provider, key string, res probe.Result, api provider.API, force bool) probe.Result {
	// The row as the check will use it: what the probe found unless the user
	// set it.
	row := p
	row.API = string(api)
	if !slices.Contains(p.Overridden, store.OverrideBackend) {
		row.Backend = string(res.Backend)
	}
	if !slices.Contains(p.Overridden, store.OverrideSlots) {
		row.Slots = res.Slots
	}
	if !namesSlots(probe.Backend(row.Backend), row.Slots) {
		return res
	}

	var prior probe.Capabilities
	if p.ProbedAt != nil {
		_ = json.Unmarshal(p.Capabilities, &prior)
	}
	model, known := s.checkModel(ctx, p.ID)
	if !force && prior.SlotAffinity != nil && prior.SlotAffinity.Current(res.Capabilities.Build, model.Model) {
		res.Capabilities.SlotAffinity = prior.SlotAffinity
		return res
	}
	priority := inference.Maintenance
	if force {
		priority = inference.Interactive
	}
	check := s.checkSlotAffinity(ctx, row, key, res, model, known, priority)
	if check.Busy && prior.SlotAffinity != nil {
		s.log.Info("slot affinity check deferred; the pool stayed busy", "provider_id", p.ID, "name", p.Name)
		res.Capabilities.SlotAffinity = prior.SlotAffinity
		return res
	}
	res.Capabilities.SlotAffinity = &check
	s.log.Info("slot affinity checked", "provider_id", p.ID, "name", p.Name, "model", model.Model,
		"verdict", check.Verdict, "reason", check.Reason, "prompt_tokens", check.Prompt, "cached_tokens", check.Cached)
	return res
}

// checkSlotAffinity runs the check against the row's endpoint with the
// provider's model. A provider with no model is skipped: nobody knows which
// model to ask.
func (s *Server) checkSlotAffinity(ctx context.Context, row store.Provider, key string, res probe.Result, model store.Model, known bool, priority inference.Priority) probe.SlotAffinity {
	now := time.Now().UTC()
	if !known {
		return probe.SlotAffinity{
			Verdict: probe.Skipped, Build: res.Capabilities.Build, CheckedAt: now,
			Reason: "no chat model is configured on this provider, so there is no model to ask",
		}
	}
	// The check names slots whatever the verdict will be: it is how the harness
	// finds out whether to.
	endpoint := providerEndpoint(row, key)
	endpoint.SlotHints = true
	client, err := s.deps.Providers.Build(row.Kind, endpoint)
	if err != nil {
		return probe.SlotAffinity{
			Verdict: probe.Failed, Model: model.Model, Build: res.Capabilities.Build, CheckedAt: now,
			Reason: scrub(err.Error(), key),
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, slotCheckTimeout)
	defer cancel()
	out := probe.CheckSlotAffinity(checkCtx, probe.SlotCheck{
		Provider: client, Model: model.Model, Build: res.Capabilities.Build,
		Queue: s.queue(ctx),
		Need:  callNeed(row, priority, "", 0),
	})
	out.Reason = scrub(out.Reason, key)
	return out
}
