package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/store"
)

// memSettings is an in-memory settings table.
type memSettings map[string]json.RawMessage

func (m memSettings) Setting(_ context.Context, key string) (store.Setting, error) {
	v, ok := m[key]
	if !ok {
		return store.Setting{}, store.ErrNotFound
	}
	return store.Setting{Key: key, Value: v}, nil
}

func (m memSettings) SetSettings(_ context.Context, values map[string]json.RawMessage) error {
	for k, v := range values {
		m[k] = v
	}
	return nil
}

func TestCanaryIsStableAcrossStartsUniquePerInstallAndStoredSealed(t *testing.T) {
	box, err := secret.Load(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	one, two := memSettings{}, memSettings{}
	first, err := loadCanary(t.Context(), one, box)
	if err != nil {
		t.Fatal(err)
	}
	again, err := loadCanary(t.Context(), one, box)
	if err != nil || again != first {
		t.Fatalf("second start: %q, %v; want %q", again, err, first)
	}
	other, err := loadCanary(t.Context(), two, box)
	if err != nil || other == first {
		t.Fatalf("another install: %q, %v; want a different value", other, err)
	}
	if strings.Contains(string(one[canarySetting]), first) {
		t.Error("the canary is stored in the clear")
	}
}
