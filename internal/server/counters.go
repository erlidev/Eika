package server

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"sync"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/store"
)

// maxSessionCounters bounds how many sessions keep a token counter. A counter
// holds the hashes and counts of what it tokenised, a few kilobytes for a long
// session, and the least recently used is dropped to make room: its session
// relearns the constants in a few calls.
const maxSessionCounters = 64

// sessionCounters keeps the token counter of each session across its runs,
// since a run is built afresh for every message and a counter that lived no
// longer would learn nothing. A counter is built from the provider row's
// probed capabilities and belongs to the endpoint it was built for: a session
// moved to another model, address, key or backend gets a new one.
//
// What is kept holds no key. The counter counts through a counter.Switch, and
// the routes, which carry the provider's key, are attached for the length of a
// run and dropped when it ends. The held fingerprint of the endpoint has a
// digest of the key, which is enough to tell that it changed.
type sessionCounters struct {
	mu sync.Mutex
	// order lists the sessions that hold a counter, least recently used first.
	order []string
	byID  map[string]heldCounter
}

// heldCounter is a session's counter, the switch its routes are attached to,
// and the endpoint it counts for.
type heldCounter struct {
	endpoint endpointPrint
	counter  *counter.Counter
	routes   *counter.Switch
}

// endpointPrint identifies the endpoint a counter counts for. It holds a digest
// of the key and never the key.
type endpointPrint struct {
	baseURL, model string
	backend        probe.Backend
	counting       probe.Counting
	keyDigest      [sha256.Size]byte
}

func newSessionCounters() *sessionCounters {
	return &sessionCounters{byID: map[string]heldCounter{}}
}

// counterConfig is the tokenizer configuration of the endpoint a run's model
// is on, from the provider row as the last probe left it. It carries the key
// and lives only as long as the run that built it.
func counterConfig(ep endpoint, model store.Model) counter.Config {
	cfg := counter.Config{
		BaseURL: ep.row.BaseURL, APIKey: ep.key, Model: model.Model,
		Backend: probe.Backend(ep.row.Backend), Counting: probe.CountNone,
	}
	var caps probe.Capabilities
	if json.Unmarshal(ep.row.Capabilities, &caps) == nil && caps.Counting != "" {
		cfg.Counting = caps.Counting
	}
	return cfg
}

// printOf is the fingerprint of the endpoint cfg describes.
func printOf(cfg counter.Config) endpointPrint {
	return endpointPrint{
		baseURL: cfg.BaseURL, model: cfg.Model, backend: cfg.Backend, counting: cfg.Counting,
		keyDigest: sha256.Sum256([]byte(cfg.APIKey)),
	}
}

// forRun returns the counter of a session for a run on ep, with its routes
// attached, and the function that ends the run's use of them: the caller
// calls it on every way out of the run, and it is safe to call twice. A
// provider that cannot state its chat body (provider.ChatEncoder) still gets
// a counter: it counts text where the endpoint can, and estimates where it
// cannot.
func (c *sessionCounters) forRun(sessionID string, ep endpoint, model store.Model) (*counter.Counter, func()) {
	cfg := counterConfig(ep, model)
	fp := printOf(cfg)
	encoder, _ := ep.client.(provider.ChatEncoder)

	c.mu.Lock()
	defer c.mu.Unlock()
	held, ok := c.byID[sessionID]
	if !ok || held.endpoint != fp {
		routes := &counter.Switch{}
		held = heldCounter{endpoint: fp, counter: counter.New(routes), routes: routes}
		if !ok && len(c.order) >= maxSessionCounters {
			delete(c.byID, c.order[0])
			c.order = c.order[1:]
		}
		c.byID[sessionID] = held
	}
	c.touch(sessionID)
	return held.counter, held.routes.Attach(counter.NewRoutes(cfg, encoder))
}

// touch marks a session's counter as the most recently used.
func (c *sessionCounters) touch(sessionID string) {
	c.order = append(slices.DeleteFunc(c.order, func(id string) bool { return id == sessionID }), sessionID)
}

// forget drops a session's counter, for a session that is deleted.
func (c *sessionCounters) forget(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byID, sessionID)
	c.order = slices.DeleteFunc(c.order, func(id string) bool { return id == sessionID })
}
