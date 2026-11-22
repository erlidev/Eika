//go:build docker

package server_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/erlidev/eika/internal/computer"
)

func TestHomeMadeBeforeSkillsGainsTheMountAtItsNextStartAndKeepsItsFiles(t *testing.T) {
	a := newAPI(t)
	// An install from before P6-03: no installer, so the home container has
	// no /skills.
	home := a.home(t)
	before, err := a.host.Inspect(t.Context(), home.ID)
	if err != nil || before.HasMount("/skills") {
		t.Fatalf("home before = %+v, %v, want no /skills", before, err)
	}
	dir, _ := a.host.dirOf(home.ID)
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("the user's work"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The upgrade: the installer can run now. A running home is not touched.
	a.host.egressControl = true
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatal(err)
	}
	if running, _ := a.host.Inspect(t.Context(), home.ID); running.HasMount("/skills") || running.ProxyToken != before.ProxyToken {
		t.Fatalf("a running home was recreated: %+v", running)
	}

	// Its next start recreates it over the same files.
	a.stopIdle(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := a.host.Inspect(t.Context(), home.ID)
	if err != nil || !after.HasMount("/skills") || after.State != computer.StateRunning {
		t.Fatalf("home after = %+v, %v, want running with /skills", after, err)
	}
	if after.ProxyToken == before.ProxyToken {
		t.Error("the recreated container kept the old proxy token")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "work.txt")); err != nil || string(data) != "the user's work" {
		t.Errorf("work.txt = %q, %v, want the file kept", data, err)
	}

	// Private computers never mount /skills.
	id, err := a.store.SkillsComputerID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if inst, err := a.host.Inspect(t.Context(), id); err != nil || inst.HasMount("/skills") {
		t.Errorf("installer = %+v, %v, want no /skills mount", inst, err)
	}
	// A second start changes nothing.
	a.stopIdle(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again, _ := a.host.Inspect(t.Context(), home.ID); again.ProxyToken != after.ProxyToken {
		t.Error("a computer that already has /skills was recreated")
	}
}
