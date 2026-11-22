//go:build docker

package server_test

import (
	"testing"
)

func TestMemoryComputerIsPrivate(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	if err := a.EnsureMemory(t.Context()); err != nil {
		t.Fatalf("EnsureMemory: %v", err)
	}
	if err := a.EnsureMemory(t.Context()); err != nil {
		t.Fatalf("EnsureMemory again: %v", err)
	}
	if a.containers() != 1 {
		t.Errorf("containers = %d, want 1", a.containers())
	}
	id, err := a.store.MemoryComputerID(t.Context())
	if err != nil || id == "" || id == "memory" {
		t.Fatalf("memory computer id = %q, %v, want a generated id", id, err)
	}
	if got := a.host.confinements(id); len(got) != 1 || !got[0].Proxied {
		t.Errorf("memory computer confinement = %+v, want one proxied", got)
	}
	if got := decodeBody[computersWire](t, request(t, a.Server, "GET", "/api/computers", nil), 200).Computers; len(got) != 0 {
		t.Errorf("computers API lists %+v, want none", got)
	}
	policy, err := a.EgressPolicy(t.Context(), id)
	if err != nil || policy.Mode != "none" {
		t.Errorf("egress policy = %+v, %v, want mode none", policy, err)
	}
	if _, err := a.MemoryRepository(t.Context()); err != nil {
		t.Errorf("MemoryRepository: %v", err)
	}
}
