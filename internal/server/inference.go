package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/store"
)

// The settings of the inference queue (docs/design/runtime.md section 3).
// Saving any of them applies it to the queue at once.
const (
	// settingPreemptRunning lets an interactive call that cannot be admitted
	// cancel running background and maintenance calls.
	settingPreemptRunning = "preempt_running"
	// settingMaintenanceIdle is how many seconds a pool must have run
	// nothing but maintenance before it admits a maintenance call.
	settingMaintenanceIdle = "maintenance_idle_seconds"
	// settingAging is how many seconds a waiting call waits for each level
	// of priority it gains.
	settingAging = "aging_seconds"
)

// The bounds of the queue's settings: a day for the idle time and a week for
// aging, past which they are the same as never.
const (
	maxMaintenanceIdleSeconds = 24 * 60 * 60
	maxAgingSeconds           = 7 * 24 * 60 * 60
)

// queuePolicy is the queue's policy as the settings state it, with the
// defaults where they state none.
func (s *Server) queuePolicy(ctx context.Context) inference.Policy {
	policy := inference.DefaultPolicy()
	var n int
	if readSetting(ctx, s.deps.Store, s.log, settingMaintenanceIdle, &n) && n >= 0 {
		policy.MaintenanceIdle = time.Duration(min(n, maxMaintenanceIdleSeconds)) * time.Second
	}
	if readSetting(ctx, s.deps.Store, s.log, settingAging, &n) && n > 0 {
		policy.Aging = time.Duration(min(n, maxAgingSeconds)) * time.Second
	}
	readSetting(ctx, s.deps.Store, s.log, settingPreemptRunning, &policy.PreemptRunning)
	return policy
}

// prefillRate is the prompt tokens a second the named endpoint has read in its
// recorded calls, which scales how long a call to it may be silent before its
// first token. Zero means nothing is recorded yet (or the store could not say):
// the agent then takes the queue's assumed rate.
func (s *Server) prefillRate(ctx context.Context, endpoint string) int {
	rate, ok, err := s.deps.Store.PrefillRate(ctx, endpoint)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("read the measured prefill rate; assuming the queue's", "endpoint", endpoint, "error", err)
		}
		return 0
	}
	if !ok {
		return 0
	}
	return rate
}

// poolEndpoint describes a stored provider to the queue.
func poolEndpoint(p store.Provider) inference.Endpoint {
	return inference.Endpoint{
		Name:     p.Name,
		Pool:     p.Pool,
		Slots:    p.Slots,
		KV:       inference.KV(p.KV),
		KVTokens: p.KVTokens,
		Window:   probedWindow(p),

		SlotHints: takesSlotHints(p),
	}
}

// syncInference makes the queue match the providers and settings as they are
// now. A change to a provider's pool, slots or cache, or to a setting of the
// queue, calls it, so it takes effect without a restart: leases already held
// run on, and what waits is admitted by the new limits.
func (s *Server) syncInference(ctx context.Context) {
	s.inferenceMu.Lock()
	defer s.inferenceMu.Unlock()
	s.syncInferenceLocked(ctx)
}

// syncInferenceLocked is syncInference with the lock held. It reports
// whether the queue now matches the store; a failure is logged and leaves the
// queue as it was.
func (s *Server) syncInferenceLocked(ctx context.Context) bool {
	if s.deps.Store == nil {
		return false
	}
	rows, err := s.deps.Store.Providers(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("list providers for the inference queue", "error", err)
		}
		return false
	}
	endpoints := make([]inference.Endpoint, 0, len(rows))
	for _, p := range rows {
		endpoints = append(endpoints, poolEndpoint(p))
	}
	s.inference.SetPools(inference.Pools(endpoints))
	s.inference.SetPolicy(s.queuePolicy(ctx))
	s.inferenceSynced = true
	return true
}

// queue returns the inference queue every model call of the server waits in.
// The first call describes it from the store, since a server that is started
// is not told of the providers until something changes them.
func (s *Server) queue(ctx context.Context) *inference.Queue {
	s.inferenceMu.Lock()
	defer s.inferenceMu.Unlock()
	if !s.inferenceSynced {
		s.syncInferenceLocked(ctx)
	}
	return s.inference
}

// callNeed describes a model call on provider p to the queue. tokens is the
// room it is expected to take: its predicted prompt plus its maximum output.
func callNeed(p store.Provider, priority inference.Priority, class string, tokens int) inference.Need {
	return inference.Need{Pool: p.PoolName(), Endpoint: p.Name, Priority: priority, Class: class, Tokens: tokens}
}

// handleInference reports the inference queue: what holds each pool and what
// waits behind it.
func (s *Server) handleInference(w http.ResponseWriter, r *http.Request) {
	q := s.queue(r.Context())
	policy := q.Policy()
	out := inferenceResponse{
		Policy: inferencePolicyBody{
			MaintenanceIdleSeconds: int(policy.MaintenanceIdle / time.Second),
			AgingSeconds:           int(policy.Aging / time.Second),
			PreemptRunning:         policy.PreemptRunning,
		},
		Pools: []inferencePoolBody{},
	}
	for _, st := range q.Snapshot() {
		pool := inferencePoolBody{
			Name:       st.Name,
			Endpoints:  st.Endpoints,
			Slots:      st.Slots,
			KV:         string(st.KV),
			UsedTokens: st.Used,
			Idle:       st.Idle,
			Running:    []inferenceRunningBody{},
			Waiting:    []inferenceWaitingBody{},
		}
		if pool.Endpoints == nil {
			pool.Endpoints = []string{}
		}
		if st.Cache > 0 {
			pool.CacheTokens = &st.Cache
		}
		if st.Idle && st.IdleFor > 0 {
			pool.IdleSeconds = new(int(st.IdleFor / time.Second))
		}
		for _, l := range st.Running {
			pool.Running = append(pool.Running, inferenceRunningBody{
				Slot: l.Slot, SlotNamed: l.Hinted, Endpoint: l.Endpoint, Class: l.Class, SessionID: l.SessionID,
				Priority: l.Priority.String(), Tokens: l.Tokens, HeldMS: l.Held.Milliseconds(), Preempted: l.Preempted,
			})
		}
		for _, c := range st.Waiting {
			pool.Waiting = append(pool.Waiting, inferenceWaitingBody{
				Endpoint: c.Endpoint, Class: c.Class, SessionID: c.SessionID,
				Priority: c.Priority.String(), Earned: c.Earned.String(), Tokens: c.Tokens,
				WaitedMS: c.Waited.Milliseconds(), TotalWaitedMS: c.WaitedSince.Milliseconds(), Gated: c.Gated,
			})
		}
		out.Pools = append(out.Pools, pool)
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// touchesInference reports whether a settings write carries a key of the
// inference queue.
func touchesInference(req map[string]json.RawMessage) bool {
	for _, key := range []string{settingPreemptRunning, settingMaintenanceIdle, settingAging} {
		if _, ok := req[key]; ok {
			return true
		}
	}
	return false
}
