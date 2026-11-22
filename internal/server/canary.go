package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
)

// canarySetting holds the install's canary, sealed. It is never shown: the
// settings API leaves it out.
const canarySetting = "sentry_canary"

// canaryStore is the part of the store the canary needs.
type canaryStore interface {
	Setting(ctx context.Context, key string) (store.Setting, error)
	SetSettings(ctx context.Context, values map[string]json.RawMessage) error
}

// loadCanary returns the install's canary. The first call makes it and stores
// it sealed; every later call, across restarts, returns the same value.
func loadCanary(ctx context.Context, st canaryStore, box *secret.Box) (string, error) {
	row, err := st.Setting(ctx, canarySetting)
	switch {
	case err == nil:
		var sealed []byte
		if err := json.Unmarshal(row.Value, &sealed); err != nil {
			return "", fmt.Errorf("read canary: %w", err)
		}
		return box.Open(sealed)
	case !errors.Is(err, store.ErrNotFound):
		return "", err
	}
	value, err := sentry.NewCanary()
	if err != nil {
		return "", err
	}
	sealed, err := box.Seal(value)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(sealed)
	if err != nil {
		return "", err
	}
	if err := st.SetSettings(ctx, map[string]json.RawMessage{canarySetting: raw}); err != nil {
		return "", err
	}
	return value, nil
}

// canaryHolder keeps the canary in memory, for the gate to read on every call.
type canaryHolder struct {
	mu    sync.RWMutex
	value string
}

func (c *canaryHolder) get() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.value
}

// ensure loads the canary once it can be, and reports whether there is one.
func (s *Server) ensureCanary(ctx context.Context) bool {
	if s.canary.get() != "" {
		return true
	}
	if s.deps.Store == nil || s.deps.Secrets == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	value, err := loadCanary(ctx, s.deps.Store, s.deps.Secrets)
	if err != nil {
		s.log.Error("load canary", "error", err)
		return false
	}
	s.canary.mu.Lock()
	s.canary.value = value
	s.canary.mu.Unlock()
	return true
}

// plantedEnv is what the home computer's environment gets beyond its egress
// settings: the canary, under the name of a real key. Other computers get none.
func (s *Server) plantedEnv(ctx context.Context, id string) []string {
	if s.deps.Store == nil || !s.ensureCanary(ctx) {
		return nil
	}
	if cx, err := s.deps.Store.Computer(ctx, id); err != nil || !cx.Home {
		return nil
	}
	return []string{sentry.CanaryName + "=" + s.canary.get()}
}
