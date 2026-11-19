//go:build docker

package server_test

import (
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
		Version      int64  `json:"version"`
	}
	for _, path := range []string{projectPath, "/api/projects/" + other.ID + "/instructions"} {
		if got := decodeBody[projectText](t, request(t, a.Server, "GET", path, nil), 200); got.Instructions != "" {
			t.Fatal(got)
		}
	}
	text := projectText{Instructions: "# Rules\nKeep this.\n", Version: 0}
	if got := decodeBody[projectText](t, request(t, a.Server, "PUT", projectPath, text), 200); got.Instructions != text.Instructions || got.Version != 1 {
		t.Fatal(got)
	}
	refused(t, request(t, a.Server, "PUT", projectPath, text), 409)
	text.Version = 1
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
	// A write that sets only part of the text is refused, not half applied.
	refused(t, request(t, a.Server, "PUT", globalPath, map[string]any{"about_me": "a"}), 400)
	if got := decodeBody[store.GlobalInstructions](t, request(t, a.Server, "GET", globalPath, nil), 200); got != global {
		t.Fatal(got)
	}
	refused(t, request(t, a.Server, "PUT", "/api/projects/missing/instructions", text), 404)
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
