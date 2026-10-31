package server

import (
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
type sessionCounters struct {
	mu sync.Mutex
	// order lists the sessions that hold a counter, least recently used first.
	order []string
	byID  map[string]heldCounter
}

// heldCounter is a session's counter and the configuration of the endpoint it
// counts for.
type heldCounter struct {
	config  counter.Config
	counter *counter.Counter
}

func newSessionCounters() *sessionCounters {
	return &sessionCounters{byID: map[string]heldCounter{}}
}

// counterConfig is the tokenizer configuration of the endpoint a run's model
// is on, from the provider row as the last probe left it.
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

// forRun returns the counter of sess for a run on ep. A provider that cannot
// state its chat body (provider.ChatEncoder) still gets one: it counts text
// where the endpoint can, and estimates where it cannot.
func (c *sessionCounters) forRun(sessionID string, ep endpoint, model store.Model) *counter.Counter {
	cfg := counterConfig(ep, model)
	c.mu.Lock()
	defer c.mu.Unlock()
	if held, ok := c.byID[sessionID]; ok && held.config == cfg {
		c.touch(sessionID)
		return held.counter
	}
	encoder, _ := ep.client.(provider.ChatEncoder)
	held := heldCounter{config: cfg, counter: counter.New(counter.NewRoutes(cfg, encoder))}
	if _, ok := c.byID[sessionID]; !ok && len(c.order) >= maxSessionCounters {
		delete(c.byID, c.order[0])
		c.order = c.order[1:]
	}
	c.byID[sessionID] = held
	c.touch(sessionID)
	return held.counter
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
