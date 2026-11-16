//go:build docker

package server_test

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
)

type projectWire struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	store.ProjectFields
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func TestProjectRoutes(t *testing.T) {
	a := newAPI(t)
	listed := decodeBody[struct {
		Projects []projectWire `json:"projects"`
	}](t, request(t, a.Server, "GET", "/api/projects", nil), 200)
	if len(listed.Projects) != 0 {
		t.Fatal(listed)
	}
	body := map[string]any{"name": "Taxes", "slug": "taxes"}
	p := decodeBody[projectWire](t, request(t, a.Server, "POST", "/api/projects", body), 201)
	if p.Directory != "/home/eika/projects/taxes" || p.Status != "active" || p.ID == "" {
		t.Fatal(p)
	}
	path := "/api/projects/" + p.ID
	decodeBody[projectWire](t, request(t, a.Server, "GET", path, nil), 200)
	refused(t, request(t, a.Server, "POST", "/api/projects", body), 409)
	updated := decodeBody[projectWire](t, request(t, a.Server, "PATCH", path, map[string]any{"name": "Tax return", "description": "This year", "status": "done", "directory": "/home/eika/existing"}), 200)
	if updated.Name != "Tax return" || updated.Description != "This year" || updated.Status != "done" || updated.Slug != p.Slug || updated.Directory != "/home/eika/existing" {
		t.Fatal(updated)
	}
	// A project's instructions have a route of their own, and its slug and
	// directory are paths the agent's tools use.
	for _, bad := range []map[string]any{{"instructions": "attack"}, {"slug": "changed"}, {"directory": "/a/../b"}} {
		refused(t, request(t, a.Server, "PATCH", path, bad), 400)
	}
	refused(t, request(t, a.Server, "POST", "/api/projects", map[string]any{"slug": "../bad", "name": "Bad"}), 400)
	refused(t, request(t, a.Server, "GET", "/api/projects/missing", nil), 404)
}
