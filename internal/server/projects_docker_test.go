//go:build docker

package server_test

import (
	"encoding/json"
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
	for _, bad := range []map[string]any{{"instructions": "attack"}, {"slug": "changed"}, {"status": "bad"}, {"status": ""}, {"directory": ""}, {"directory": "/a/../b"}, {"name": ""}, {"directory": "/a\nb"}} {
		refused(t, request(t, a.Server, "PATCH", path, bad), 400)
	}
	for _, bad := range []map[string]any{{"slug": "../bad", "name": "Bad"}, {"slug": "UPPER", "name": "Bad"}, {"slug": "empty"}, {"slug": "rules", "name": "Bad", "instructions": "attack"}} {
		refused(t, request(t, a.Server, "POST", "/api/projects", bad), 400)
	}
	for _, method := range []string{"POST", "PATCH"} {
		target := "/api/projects"
		if method == "PATCH" {
			target = path
		}
		refused(t, request(t, a.Server, method, target, json.RawMessage(`null`)), 400)
	}
	for _, method := range []string{"GET", "PATCH"} {
		refused(t, request(t, a.Server, method, "/api/projects/missing", map[string]any{}), 404)
	}
	for _, route := range []struct{ method, path string }{{"GET", "/api/projects"}, {"POST", "/api/projects"}, {"GET", path}, {"PATCH", path}} {
		refused(t, requestWith(t, a.Server, route.method, route.path, body, ""), 401)
	}
}
