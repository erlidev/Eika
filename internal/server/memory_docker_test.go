//go:build docker

package server_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/memory"
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

type memoryWire struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Pinned     bool   `json:"pinned"`
	Body       string `json:"body"`
	ContentSHA string `json:"content_sha"`
	Reason     string `json:"archive_reason"`
}

func TestUserMemoryAPI(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	if err := a.RecoverMemory(t.Context()); err != nil {
		t.Fatalf("RecoverMemory: %v", err)
	}
	for _, name := range []string{"units", "tea"} {
		if _, err := a.MemoryWriter().Write(t.Context(), memory.Request{Name: name, Type: "preference", Body: "metric units " + name,
			Description: "Use when the user asks about measurements.", Source: "session s1"}); err != nil {
			t.Fatal(err)
		}
	}
	page := decodeBody[struct {
		Memories   []memoryWire `json:"memories"`
		NextCursor string       `json:"next_cursor"`
	}](t, request(t, a.Server, "GET", "/api/memories?limit=1", nil), 200)
	if len(page.Memories) != 1 || page.NextCursor == "" {
		t.Fatalf("first page = %+v", page)
	}
	next := decodeBody[struct {
		Memories   []memoryWire `json:"memories"`
		NextCursor string       `json:"next_cursor"`
	}](t, request(t, a.Server, "GET", "/api/memories?limit=1&after="+page.NextCursor, nil), 200)
	if len(next.Memories) != 1 || next.Memories[0].Name == page.Memories[0].Name || next.NextCursor != "" {
		t.Fatalf("second page = %+v", next)
	}
	if got := decodeBody[struct {
		Memories []memoryWire `json:"memories"`
	}](t, request(t, a.Server, "GET", "/api/memories?q=metric", nil), 200); len(got.Memories) != 2 {
		t.Fatalf("search = %+v", got)
	}
	if rec := request(t, a.Server, "GET", "/api/memories?limit=0", nil); rec.Code != 400 {
		t.Fatalf("limit 0 = %d", rec.Code)
	}

	m := decodeBody[memoryWire](t, request(t, a.Server, "GET", "/api/memories/units", nil), 200)
	edited := decodeBody[memoryWire](t, request(t, a.Server, "PATCH", "/api/memories/units", map[string]any{"content_sha": m.ContentSHA, "body": "imperial units"}), 200)
	if edited.Body != "imperial units" || edited.State != "active" || edited.ContentSHA == m.ContentSHA {
		t.Fatalf("edit = %+v", edited)
	}
	if rec := request(t, a.Server, "PATCH", "/api/memories/units", map[string]any{"content_sha": m.ContentSHA, "body": "again"}); rec.Code != 409 {
		t.Fatalf("stale edit = %d: %s", rec.Code, rec.Body.String())
	}
	// A credential is refused whoever writes it, and nothing changes.
	rec := request(t, a.Server, "PATCH", "/api/memories/units", map[string]any{"content_sha": edited.ContentSHA, "body": "key AKIAABCDEFGHIJKLMNOP"})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "credential") {
		t.Fatalf("credential edit = %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeBody[memoryWire](t, request(t, a.Server, "GET", "/api/memories/units", nil), 200); got.Body != "imperial units" {
		t.Fatalf("denied edit changed the body to %q", got.Body)
	}
	// Without an exact token count a pin is refused, never guessed.
	if rec := request(t, a.Server, "POST", "/api/memories/units/pin", nil); rec.Code != 503 {
		t.Fatalf("pin without exact count = %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeBody[memoryWire](t, request(t, a.Server, "POST", "/api/memories/units/unpin", nil), 200); got.Pinned {
		t.Fatalf("unpin = %+v", got)
	}
	arch := decodeBody[memoryWire](t, request(t, a.Server, "POST", "/api/memories/units/archive", map[string]any{"reason": "outdated"}), 200)
	if arch.State != "archived" || arch.Reason != "outdated" {
		t.Fatalf("archive = %+v", arch)
	}
	if rec := request(t, a.Server, "DELETE", "/api/memories/units?content_sha=stale", nil); rec.Code != 409 {
		t.Fatalf("stale purge = %d", rec.Code)
	}
	if rec := request(t, a.Server, "DELETE", "/api/memories/units?content_sha="+arch.ContentSHA, nil); rec.Code != 204 {
		t.Fatalf("purge = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(t, a.Server, "GET", "/api/memories/units", nil); rec.Code != 404 {
		t.Fatalf("read after purge = %d", rec.Code)
	}
}
