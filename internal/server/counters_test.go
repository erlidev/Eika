package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/provider/probe"
	"github.com/erlidev/eika/internal/store"
)

// secretKey is a key no other test value contains.
const secretKey = "sk-counter-test-5f0c1d9e7a"

// endpointOn returns the endpoint a run on a llama.cpp server at url would
// use, with the capabilities a probe leaves.
func endpointOn(t *testing.T, url, key string, counting probe.Counting) endpoint {
	t.Helper()
	caps, err := json.Marshal(probe.Capabilities{Counting: counting, CacheMetrics: probe.CacheUnknown})
	if err != nil {
		t.Fatalf("encode capabilities: %v", err)
	}
	client, err := openai.New(provider.Endpoint{BaseURL: url, APIKey: key})
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	return endpoint{
		row:    store.Provider{Name: "local", BaseURL: url, Backend: string(probe.LlamaCpp), Capabilities: caps},
		key:    key,
		client: client,
	}
}

// mentions reports whether key is in v or anything reachable from it, in a
// string or a byte slice, through unexported fields, pointers, maps and
// interfaces alike.
func mentions(v reflect.Value, key string, seen map[[2]uintptr]bool) bool {
	switch v.Kind() {
	case reflect.String:
		return strings.Contains(v.String(), key)
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, v.Len())
			for i := range b {
				b[i] = byte(v.Index(i).Uint())
			}
			return strings.Contains(string(b), key)
		}
		if v.Kind() == reflect.Slice {
			if v.IsNil() || seen[[2]uintptr{v.Pointer(), uintptr(v.Len())}] {
				return false
			}
			seen[[2]uintptr{v.Pointer(), uintptr(v.Len())}] = true
		}
		for i := range v.Len() {
			if mentions(v.Index(i), key, seen) {
				return true
			}
		}
	case reflect.Pointer:
		if v.IsNil() || seen[[2]uintptr{v.Pointer(), 1}] {
			return false
		}
		seen[[2]uintptr{v.Pointer(), 1}] = true
		return mentions(v.Elem(), key, seen)
	case reflect.Interface:
		return !v.IsNil() && mentions(v.Elem(), key, seen)
	case reflect.Map:
		if v.IsNil() || seen[[2]uintptr{v.Pointer(), 2}] {
			return false
		}
		seen[[2]uintptr{v.Pointer(), 2}] = true
		for it := v.MapRange(); it.Next(); {
			if mentions(it.Key(), key, seen) || mentions(it.Value(), key, seen) {
				return true
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if mentions(v.Field(i), key, seen) {
				return true
			}
		}
	}
	return false
}

func reaches(root any, key string) bool {
	return mentions(reflect.ValueOf(root), key, map[[2]uintptr]bool{})
}

// Between runs the held counters know the endpoint only by a digest of its key.
// While a run holds the routes they carry the key, and when it ends, by any
// way, nothing reachable from the registry does.
func TestNothingHeldBetweenRunsContainsTheKey(t *testing.T) {
	counters := newSessionCounters()
	model := store.Model{Model: "qwen"}
	ep := endpointOn(t, "http://127.0.0.1:1/v1", secretKey, probe.CountChat)

	c, release := counters.forRun("s1", ep, model)
	if !counters.byID["s1"].routes.Attached() {
		t.Fatal("the run's routes are not attached")
	}
	if !reaches(counters, secretKey) {
		t.Fatal("the key is not reachable while a run holds the routes, so the walk below proves nothing")
	}
	release()
	release() // ending a run twice, as an abandoned reservation and a finished run might
	if reaches(counters, secretKey) {
		t.Error("the registry still reaches the key after the run ended")
	}
	if reaches(c, secretKey) {
		t.Error("the counter still reaches the key after the run ended")
	}
	if counters.byID["s1"].routes.Attached() {
		t.Error("routes are still attached after the run ended")
	}

	// A changed key is a new counter, and the old key is gone from it too.
	changed, release := counters.forRun("s1", endpointOn(t, "http://127.0.0.1:1/v1", secretKey+"-rotated", probe.CountChat), model)
	release()
	if changed == c {
		t.Error("a changed key kept the counter")
	}
	if reaches(counters, secretKey) {
		t.Error("the registry reaches a key after it was replaced")
	}
	// The same key again is the same counter, as before.
	same, release := counters.forRun("s1", endpointOn(t, "http://127.0.0.1:1/v1", secretKey+"-rotated", probe.CountChat), model)
	release()
	if same != changed {
		t.Error("the same key got a new counter")
	}
}

// A reservation that never became a run, and a run that finished, both end
// their use of the counter's routes; a run that has no counter yet is safe.
func TestEveryWayOutOfARunEndsItsUseOfTheCounterRoutes(t *testing.T) {
	s := &Server{counters: newSessionCounters()}
	s.idle = newIdleComputers(s)
	s.runs = newRuns(s)
	ep := endpointOn(t, "http://127.0.0.1:1/v1", secretKey, probe.CountChat)

	attach := func() (*activeRun, interface{ Attached() bool }) {
		_, release := s.counters.forRun("s1", ep, store.Model{Model: "m"})
		active := &activeRun{sessionID: "s1", cancel: func() {}, done: make(chan struct{}), ready: make(chan struct{}), releaseCounter: release}
		return active, s.counters.byID["s1"].routes
	}

	active, routes := attach()
	s.runs.abandon(active)
	if routes.Attached() {
		t.Error("an abandoned reservation kept the routes")
	}

	// drive defers this method first thing, so it runs on every way out of it.
	active, routes = attach()
	active.releaseCounterRoutes()
	if routes.Attached() {
		t.Error("a finished run kept the routes")
	}

	(&activeRun{}).releaseCounterRoutes() // no counter yet: nothing to do
}
