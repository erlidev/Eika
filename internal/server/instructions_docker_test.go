//go:build docker

package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestInstructionRoutes(t *testing.T) {
	a := newAPI(t)
	globalPath := "/api/instructions"
	global := store.GlobalInstructions{AboutMe: "About me\n", Rules: "Use metric"}
	got := decodeBody[store.GlobalInstructions](t, request(t, a.Server, "GET", globalPath, nil), 200)
	if got != (store.GlobalInstructions{}) {
		t.Fatal(got)
	}
	got = decodeBody[store.GlobalInstructions](t, request(t, a.Server, "PUT", globalPath, global), 200)
	if got != global {
		t.Fatal(got)
	}
	p := decodeBody[projectWire](t, request(t, a.Server, "POST", "/api/projects", map[string]any{"name": "One", "slug": "one"}), 201)
	other := decodeBody[projectWire](t, request(t, a.Server, "POST", "/api/projects", map[string]any{"name": "Two", "slug": "two"}), 201)
	projectPath := "/api/projects/" + p.ID + "/instructions"
	type projectText struct {
		Instructions string `json:"instructions"`
	}
	for _, path := range []string{projectPath, "/api/projects/" + other.ID + "/instructions"} {
		if got := decodeBody[projectText](t, request(t, a.Server, "GET", path, nil), 200); got.Instructions != "" {
			t.Fatal(got)
		}
	}
	text := projectText{Instructions: "# Rules\nKeep this.\n"}
	if got := decodeBody[projectText](t, request(t, a.Server, "PUT", projectPath, text), 200); got != text {
		t.Fatal(got)
	}
	decodeBody[projectWire](t, request(t, a.Server, "PATCH", "/api/projects/"+p.ID, map[string]any{"description": "Changed"}), 200)
	refused(t, request(t, a.Server, "PATCH", "/api/projects/"+p.ID, map[string]any{"instructions": "injected"}), 400)
	if got := decodeBody[projectText](t, request(t, a.Server, "GET", projectPath, nil), 200); got != text {
		t.Fatal(got)
	}
	if got := decodeBody[store.GlobalInstructions](t, request(t, a.Server, "GET", globalPath, nil), 200); got != global {
		t.Fatal(got)
	}
	if got := decodeBody[projectText](t, request(t, a.Server, "GET", "/api/projects/"+other.ID+"/instructions", nil), 200); got.Instructions != "" {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		path  string
		valid any
		bad   []any
	}{
		{globalPath, global, []any{nil, json.RawMessage(`null`), map[string]any{}, map[string]any{"about_me": "a"}, map[string]any{"about_me": "a", "rules": nil}, map[string]any{"about_me": "a", "rules": 4}, map[string]any{"about_me": "a", "rules": "\x00"}, map[string]any{"about_me": "a", "rules": strings.Repeat("a", 65537)}, map[string]any{"about_me": "a", "rules": "b", "project_id": p.ID}}},
		{projectPath, text, []any{json.RawMessage(`null`), map[string]any{}, map[string]any{"instructions": nil}, map[string]any{"instructions": 42}, projectText{Instructions: "\x00"}, projectText{Instructions: strings.Repeat("a", 65537)}, map[string]any{"instructions": "a", "rules": "b"}}},
	} {
		for _, bad := range tc.bad {
			refused(t, request(t, a.Server, "PUT", tc.path, bad), 400)
		}
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			refused(t, requestWith(t, a.Server, method, tc.path, tc.valid, ""), 401)
		}
	}
	if got := decodeBody[projectText](t, request(t, a.Server, "GET", projectPath, nil), 200); got != text {
		t.Fatal(got)
	}
	if got := decodeBody[store.GlobalInstructions](t, request(t, a.Server, "GET", globalPath, nil), 200); got != global {
		t.Fatal(got)
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		refused(t, request(t, a.Server, method, "/api/projects/missing/instructions", text), 404)
	}
	for _, path := range []string{globalPath, projectPath} {
		for range 2 {
			resp := request(t, a.Server, "DELETE", path, nil)
			if resp.Code != 204 {
				t.Fatalf("delete: %d", resp.Code)
			}
		}
	}
	if got := decodeBody[projectText](t, request(t, a.Server, "GET", projectPath, nil), 200); got.Instructions != "" {
		t.Fatal(got)
	}
	if got := decodeBody[store.GlobalInstructions](t, request(t, a.Server, "GET", globalPath, nil), 200); got != (store.GlobalInstructions{}) {
		t.Fatal(got)
	}
}
