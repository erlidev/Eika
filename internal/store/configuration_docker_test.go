//go:build docker

package store_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestProvidersAndModels(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	p, err := st.CreateProvider(ctx, store.Provider{
		Name: "OpenAI", Kind: "openai", BaseURL: "https://api.openai.com/v1", APIKey: []byte("sealed"),
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	if _, err := st.CreateProvider(ctx, store.Provider{Name: "OpenAI", Kind: "openai", BaseURL: "x"}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate provider name = %v, want ErrConflict", err)
	}

	_, err = st.CreateModel(ctx, store.Model{
		ProviderID: p.ID, Name: "gpt-5", Model: "gpt-5", ContextWindow: 400000, MaxOutput: 128000,
		ReasoningEffort: "medium",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if _, err := st.CreateModel(ctx, store.Model{
		ProviderID: p.ID, Name: "gpt-5", Model: "other", ContextWindow: 1, MaxOutput: 1,
	}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate model name = %v, want ErrConflict", err)
	}
	if _, err := st.CreateModel(ctx, store.Model{
		ProviderID: "absent", Name: "orphan", Model: "x", ContextWindow: 1, MaxOutput: 1,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("model of an unknown provider = %v, want ErrNotFound", err)
	}

	// Deleting a provider takes its models with it.
	if err := st.DeleteProvider(ctx, p.ID); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if models, err := st.Models(ctx); err != nil || len(models) != 0 {
		t.Errorf("Models after deleting the provider = %+v, %v", models, err)
	}
	if err := st.DeleteProvider(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second DeleteProvider = %v, want ErrNotFound", err)
	}
}

// A probe fills the probed fields; a field the user set is left alone by the
// next probe until it is handed back; a failed probe keeps the earlier result.
func TestProbeResultsAndOverrides(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	p, err := st.CreateProvider(ctx, store.Provider{Name: "bench", Kind: "openai", BaseURL: "http://m.invalid/v1"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	found := store.ProbeResult{
		Backend: "llamacpp", Slots: 2, KV: "unknown", Capabilities: []byte(`{"window":131072}`), At: at,
	}
	got, err := st.RecordProbe(ctx, p.ID, found)
	if err != nil {
		t.Fatalf("RecordProbe: %v", err)
	}
	if got.Backend != "llamacpp" || got.Slots != 2 || got.KV != "unknown" || got.ProbedAt == nil || !got.ProbedAt.Equal(at) ||
		!sameJSON(t, got.Capabilities, []byte(`{"window":131072}`)) {
		t.Errorf("after a probe = %+v", got)
	}

	// The user states what the probe could not.
	got, err = st.OverrideProvider(ctx, p.ID, store.ProviderOverride{
		KV: ptr("shared"), KVTokens: ptr(131072), Pool: ptr("rtx5090"),
	})
	if err != nil {
		t.Fatalf("OverrideProvider: %v", err)
	}
	if got.KV != "shared" || got.KVTokens != 131072 || got.PoolName() != "rtx5090" || !slices.Equal(got.Overridden, []string{"kv"}) {
		t.Errorf("after an override = %+v", got)
	}

	// The next probe replaces what it owns and keeps what the user set.
	found.Slots, found.KV, found.At = 4, "partitioned", at.Add(time.Hour)
	got, err = st.RecordProbe(ctx, p.ID, found)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slots != 4 || got.KV != "shared" || got.KVTokens != 131072 || got.PoolName() != "rtx5090" {
		t.Errorf("after a second probe = %+v, want slots 4 and the user's cache and pool", got)
	}

	// A failed probe records why and loses nothing; a success clears it.
	if err := st.RecordProbeFailure(ctx, p.ID, "no route to host"); err != nil {
		t.Fatalf("RecordProbeFailure: %v", err)
	}
	got, _ = st.Provider(ctx, p.ID)
	if got.ProbeError != "no route to host" || got.Slots != 4 || got.Backend != "llamacpp" || !got.ProbedAt.Equal(at.Add(time.Hour)) {
		t.Errorf("after a failed probe = %+v", got)
	}
	if got, _ = st.RecordProbe(ctx, p.ID, found); got.ProbeError != "" {
		t.Errorf("probe error after a success = %q", got.ProbeError)
	}

	// Handing a field back lets the probe write it again, and a cache that is
	// not shared has no size.
	got, err = st.OverrideProvider(ctx, p.ID, store.ProviderOverride{Reset: []string{"kv", "pool"}})
	if err != nil || len(got.Overridden) != 0 || got.PoolName() != "bench" {
		t.Errorf("after a reset = %+v, %v", got, err)
	}
	got, _ = st.RecordProbe(ctx, p.ID, found)
	if got.KV != "partitioned" || got.KVTokens != 0 {
		t.Errorf("after the reset and a probe: kv %q, %d tokens", got.KV, got.KVTokens)
	}
	got, err = st.OverrideProvider(ctx, p.ID, store.ProviderOverride{KV: ptr("shared"), KVTokens: ptr(1000)})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ = st.OverrideProvider(ctx, p.ID, store.ProviderOverride{KV: ptr("partitioned")}); got.KVTokens != 0 {
		t.Errorf("a partitioned cache kept %d tokens", got.KVTokens)
	}

	if _, err := st.OverrideProvider(ctx, "absent", store.ProviderOverride{Slots: ptr(2)}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("override of an unknown provider = %v, want ErrNotFound", err)
	}
	if err := st.RecordProbeFailure(ctx, "absent", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("failure of an unknown provider = %v, want ErrNotFound", err)
	}
}

func TestPasswordAndSessions(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()

	if _, err := st.PasswordHash(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("PasswordHash before setup = %v, want ErrNotFound", err)
	}
	if err := st.CreatePasswordHash(ctx, "first"); err != nil {
		t.Fatalf("CreatePasswordHash: %v", err)
	}
	if err := st.CreatePasswordHash(ctx, "second"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("second CreatePasswordHash = %v, want ErrConflict", err)
	}

	live := time.Now().Add(time.Hour)
	if err := st.CreateAuthSession(ctx, "old", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if err := st.CreateAuthSession(ctx, "live", live); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := st.AuthSessionExpiry(ctx, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an expired session survived creating another: %v", err)
	}
	if got, err := st.AuthSessionExpiry(ctx, "live"); err != nil || got.Sub(live).Abs() > time.Millisecond {
		t.Errorf("AuthSessionExpiry = %v, %v; want %v", got, err, live)
	}

	// A new password ends every session.
	if err := st.ReplacePasswordHash(ctx, "changed"); err != nil {
		t.Fatalf("ReplacePasswordHash: %v", err)
	}
	if hash, err := st.PasswordHash(ctx); err != nil || hash != "changed" {
		t.Errorf("PasswordHash = %q, %v", hash, err)
	}
	if _, err := st.AuthSessionExpiry(ctx, "live"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a session survived the password change: %v", err)
	}
	if err := st.DeleteAuthSession(ctx, "live"); err != nil {
		t.Errorf("deleting a session that is gone = %v, want nil", err)
	}
}
