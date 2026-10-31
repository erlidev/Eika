package inference

import (
	"cmp"
	"slices"
	"time"
)

// KV says how a pool's KV cache is divided among its requests, as the
// provider rows state it.
type KV string

// The ways a KV cache is divided.
const (
	// KVShared is one cache all requests draw on.
	KVShared KV = "shared"
	// KVPartitioned is a fixed share per slot, so slots alone limit
	// admission.
	KVPartitioned KV = "partitioned"
	// KVUnknown is a server that does not say. It is admitted as a shared
	// cache the size of one request's window, which cannot overfill a unified
	// cache and keeps every request inside its share of a partitioned one.
	KVUnknown KV = "unknown"
)

// Endpoint is what one provider row says about the hardware behind it.
type Endpoint struct {
	// Name is the provider's name, for display.
	Name string
	// Pool is the hardware group; empty means Name.
	Pool string
	// Slots is the concurrent requests the endpoint takes; below one counts
	// as one.
	Slots int
	// KV is how the cache is divided, and KVTokens the size of a shared one,
	// zero when the server does not say.
	KV       KV
	KVTokens int
	// Window is the per-request context the probe reported, zero when it
	// stated none. An unknown KV is admitted against it.
	Window int
	// SlotHints reports that the server takes a request naming the slot it
	// runs on (llama.cpp with several slots), and Slots is then the number of
	// slots the server has. A slot number means something on one server only,
	// so the queue keeps the endpoint's own books.
	SlotHints bool
}

// poolName is the pool the endpoint belongs to.
func (e Endpoint) poolName() string {
	if e.Pool != "" {
		return e.Pool
	}
	return e.Name
}

// budget is the cache the endpoint lets calls fill, in tokens, and zero when
// nothing but slots limits admission.
func (e Endpoint) budget() int {
	switch e.KV {
	case KVShared:
		return max(e.KVTokens, 0)
	case KVUnknown:
		return max(e.Window, 0)
	}
	return 0
}

// Pool is one hardware group as the queue runs it.
type Pool struct {
	// Name identifies the pool; calls name it in Need.Pool.
	Name string
	// Endpoints are the providers in the pool, for display.
	Endpoints []string
	// Slots is how many leases the pool admits at once.
	Slots int
	// KV is how the pool's cache is divided.
	KV KV
	// Cache is the tokens running calls may occupy together, each counted at
	// its predicted prompt plus maximum output. Zero means only Slots limit
	// admission.
	Cache int
	// Hinted are the endpoints of the pool that take slot hints, by name. Each
	// has slot numbers of its own: the pool's Slots limit how many calls run
	// at once on all of them, and a hinted endpoint may have more slots than
	// that, to keep more sessions' prefixes warm.
	Hinted []SlotEndpoint
}

// Pools groups endpoints into pools, merging the rows of one pool by taking
// the most restrictive of what they say, since a pool is one machine and a
// row that claims more than another is not believed over it: its slots are
// the fewest any row states, its cache the smallest size any row gives, and
// its KV the most binding mode among the rows (shared, then unknown, then
// partitioned). The pools come back sorted by name.
func Pools(endpoints []Endpoint) []Pool {
	byName := map[string]*Pool{}
	for _, e := range endpoints {
		name := e.poolName()
		p, ok := byName[name]
		slots := max(e.Slots, 1)
		if !ok {
			p = &Pool{Name: name, Slots: slots, KV: e.KV}
			byName[name] = p
		}
		p.Endpoints = append(p.Endpoints, e.Name)
		p.Slots = min(p.Slots, slots)
		p.KV = mostBinding(p.KV, e.KV)
		if b := e.budget(); b > 0 && (p.Cache == 0 || b < p.Cache) {
			p.Cache = b
		}
		if e.SlotHints && slots > 1 {
			p.Hinted = append(p.Hinted, SlotEndpoint{Name: e.Name, Slots: slots})
		}
	}
	out := make([]Pool, 0, len(byName))
	for _, p := range byName {
		slices.Sort(p.Endpoints)
		slices.SortFunc(p.Hinted, func(a, b SlotEndpoint) int { return cmp.Compare(a.Name, b.Name) })
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b Pool) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// mostBinding returns the mode of a and b that limits admission more.
func mostBinding(a, b KV) KV {
	rank := func(k KV) int {
		switch k {
		case KVShared:
			return 2
		case KVUnknown:
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// The defaults of the policy, which the settings of the same names override.
const (
	// DefaultMaintenanceIdle is how long a pool must have had no other work
	// before a maintenance call runs.
	DefaultMaintenanceIdle = 120 * time.Second
	// DefaultAging is how long a call waits for each priority level it gains.
	DefaultAging = 300 * time.Second
)

// Policy is how the queue orders and admits calls.
type Policy struct {
	// MaintenanceIdle is how long a pool must have gone without running a
	// call of a higher priority before it admits a maintenance one. Zero
	// admits maintenance calls at once, in order.
	MaintenanceIdle time.Duration
	// Aging is the wait that raises a waiting call one level. It must be
	// positive; zero means DefaultAging.
	Aging time.Duration
	// PreemptRunning lets an interactive call that cannot be admitted cancel
	// running background and maintenance calls, which their callers retry.
	PreemptRunning bool
	// PrefillRate is the prompt tokens per second a server is assumed to read,
	// which prices a session's prefix: a call waits for the busy slot that
	// holds it for as long as reading it again would take. Zero means
	// DefaultPrefillRate.
	PrefillRate int
}

// DefaultPolicy returns the policy the settings start with.
func DefaultPolicy() Policy {
	return Policy{MaintenanceIdle: DefaultMaintenanceIdle, Aging: DefaultAging, PrefillRate: DefaultPrefillRate}
}

// normalised returns p with its fields made usable.
func (p Policy) normalised() Policy {
	if p.Aging <= 0 {
		p.Aging = DefaultAging
	}
	if p.PrefillRate <= 0 {
		p.PrefillRate = DefaultPrefillRate
	}
	p.MaintenanceIdle = max(p.MaintenanceIdle, 0)
	return p
}
