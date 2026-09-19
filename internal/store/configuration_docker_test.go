//go:build docker

package store_test

import (
	"errors"
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
	if p.ID == "" || string(p.APIKey) != "sealed" || p.CreatedAt.IsZero() {
		t.Errorf("provider = %+v", p)
	}
	if _, err := st.CreateProvider(ctx, store.Provider{Name: "OpenAI", Kind: "openai", BaseURL: "x"}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate provider name = %v, want ErrConflict", err)
	}

	m, err := st.CreateModel(ctx, store.Model{
		ProviderID: p.ID, Name: "gpt-5", Model: "gpt-5", ContextWindow: 400000, MaxOutput: 128000,
		ReasoningEffort: "medium",
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if got, err := st.ModelByName(ctx, "gpt-5"); err != nil || got.ID != m.ID || got.ReasoningEffort != "medium" {
		t.Errorf("ModelByName = %+v, %v", got, err)
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

	m.Name, m.MaxOutput, m.PreserveThinking = "gpt-5-high", 64000, true
	updated, err := st.UpdateModel(ctx, m)
	if err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	if updated.Name != "gpt-5-high" || updated.MaxOutput != 64000 || !updated.PreserveThinking {
		t.Errorf("updated model = %+v", updated)
	}

	p.Name, p.APIKey = "OpenAI main", nil
	if got, err := st.UpdateProvider(ctx, p); err != nil || got.Name != "OpenAI main" || got.APIKey != nil {
		t.Errorf("UpdateProvider = %+v, %v", got, err)
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

func TestProjectCredentialsUpdate(t *testing.T) {
	st := storetest.Open(t)
	p := newProject(t, st)
	p.RemoteUsername, p.RemotePassword, p.DefaultBranch = "x-access-token", []byte("sealed"), "trunk"
	got, err := st.UpdateProject(t.Context(), p)
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if got.RemoteUsername != "x-access-token" || string(got.RemotePassword) != "sealed" || got.DefaultBranch != "trunk" {
		t.Errorf("project = %+v", got)
	}
	if _, err := st.UpdateProject(t.Context(), store.Project{ID: "absent"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UpdateProject of an unknown project = %v, want ErrNotFound", err)
	}
}
