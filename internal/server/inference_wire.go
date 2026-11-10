package server

// inferenceResponse is the body of GET /api/inference: the inference queue as
// it stands, what holds each pool and what waits behind it.
type inferenceResponse struct {
	// Policy is how the queue orders and admits calls now.
	Policy inferencePolicyBody `json:"policy"`
	// Pools are the hardware groups, by name: every pool the providers
	// describe, and any other that still has a call.
	Pools []inferencePoolBody `json:"pools"`
}

// inferencePolicyBody is the policy in force.
type inferencePolicyBody struct {
	// MaintenanceIdleSeconds is how long a pool must have run nothing but
	// maintenance before it admits a maintenance call. Zero means no wait.
	MaintenanceIdleSeconds int `json:"maintenance_idle_seconds"`
	// AgingSeconds is how long a waiting call waits for each priority level
	// it gains.
	AgingSeconds int `json:"aging_seconds"`
	// PreemptRunning is whether an interactive call that cannot be admitted
	// cancels running background and maintenance calls.
	PreemptRunning bool `json:"preempt_running"`
}

// inferencePoolBody is one pool.
type inferencePoolBody struct {
	Name string `json:"name"`
	// Endpoints are the providers in the pool, which are serialised against
	// each other.
	Endpoints []string `json:"endpoints"`
	// Slots is how many calls the pool runs at once.
	Slots int `json:"slots"`
	// KV is how the pool's cache is divided: shared, partitioned or unknown.
	KV string `json:"kv"`
	// CacheTokens is the cache a call is admitted against, in tokens: the
	// size of a shared cache, or the window for an unknown one. Null when
	// only slots limit admission.
	CacheTokens *int `json:"cache_tokens"`
	// UsedTokens is the sum of the running calls' predicted prompt plus
	// maximum output, which CacheTokens bounds.
	UsedTokens int `json:"used_tokens"`
	// Idle is whether the pool has run nothing but maintenance for the idle
	// time, so a maintenance call is admitted. IdleSeconds is how long it has
	// been idle, null when it has never run anything else.
	Idle        bool `json:"idle"`
	IdleSeconds *int `json:"idle_seconds"`
	// Running are the calls holding a slot, by slot.
	Running []inferenceRunningBody `json:"running"`
	// Waiting are the calls waiting, in the order they will be admitted.
	Waiting []inferenceWaitingBody `json:"waiting"`
}

// inferenceRunningBody is a call holding a slot.
type inferenceRunningBody struct {
	// Slot is the slot held. SlotNamed says it is the server's own slot
	// number, which the call named (id_slot) so that it runs where its
	// session's prefix is; otherwise it is the queue's count of the pool's
	// slots.
	Slot      int    `json:"slot"`
	SlotNamed bool   `json:"slot_named"`
	Endpoint  string `json:"endpoint"`
	Class     string `json:"class,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// Priority is what the call asked for.
	Priority string `json:"priority"`
	// Tokens is the call's predicted prompt plus maximum output; 0 when not
	// known.
	Tokens int `json:"tokens"`
	// HeldMS is how long it has held the slot.
	HeldMS int64 `json:"held_ms"`
	// Preempted is whether it has been cancelled for an interactive call and
	// has not yet let go.
	Preempted bool `json:"preempted"`
}

// inferenceWaitingBody is a call waiting for a slot.
type inferenceWaitingBody struct {
	Endpoint  string `json:"endpoint"`
	Class     string `json:"class,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// Priority is what the call asked for and Earned what its waiting has
	// raised it to.
	Priority string `json:"priority"`
	Earned   string `json:"earned"`
	Tokens   int    `json:"tokens"`
	// WaitedMS is how long it has waited for this slot, and TotalWaitedMS
	// how long since the caller first began waiting for the call, which
	// differs after a retry.
	WaitedMS      int64 `json:"waited_ms"`
	TotalWaitedMS int64 `json:"total_waited_ms"`
	// Gated is whether it is not eligible yet: a maintenance call waiting
	// for an idle pool.
	Gated bool `json:"gated"`
}
