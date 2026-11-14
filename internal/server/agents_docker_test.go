//go:build docker

package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agentdef"
)

type agentWire struct {
	agentdef.Definition
	Builtin   agentdef.Definition `json:"builtin"`
	Overrides agentdef.Override   `json:"overrides"`
}

func TestAgentRoutes(t *testing.T) {
	a := newAPI(t)
	listed := decodeBody[struct {
		Agents []agentWire `json:"agents"`
	}](t, request(t, a.Server, "GET", "/api/agents", nil), 200)
	if len(listed.Agents) != 3 {
		t.Fatal(listed)
	}
	for _, d := range listed.Agents {
		if d.Prompt != d.Builtin.Prompt || d.Class != d.Builtin.Class || d.Overrides.Class != nil || d.Overrides.Prompt != nil {
			t.Errorf("default: %+v", d)
		}
	}
	decodeBody[agentWire](t, request(t, a.Server, "GET", "/api/agents/eika", nil), 200)
	updated := decodeBody[agentWire](t, request(t, a.Server, "PUT", "/api/agents/eika", map[string]any{"class": "deep", "prompt": ""}), 200)
	if updated.Class != "deep" || updated.Prompt != "" || updated.Overrides.Prompt == nil || updated.Builtin.Prompt == "" {
		t.Fatal(updated)
	}
	updated = decodeBody[agentWire](t, request(t, a.Server, "PUT", "/api/agents/eika", map[string]any{"class": nil, "prompt": "custom"}), 200)
	if updated.Class != "main" || updated.Prompt != "custom" || updated.Overrides.Class != nil {
		t.Fatal(updated)
	}
	// The PUT replaces the whole override; omitted fields reset as well.
	updated = decodeBody[agentWire](t, request(t, a.Server, "PUT", "/api/agents/eika", map[string]any{}), 200)
	if updated.Class != "main" || updated.Prompt != updated.Builtin.Prompt {
		t.Fatal(updated)
	}
	decodeBody[agentWire](t, request(t, a.Server, "PUT", "/api/agents/worker", map[string]any{"class": "fast"}), 200)
	rec := request(t, a.Server, "DELETE", "/api/agents/worker", nil)
	if rec.Code != 204 {
		t.Fatal(rec.Body.String())
	}
	reset := decodeBody[agentWire](t, request(t, a.Server, "GET", "/api/agents/worker", nil), 200)
	if reset.Class != "main" || reset.Overrides.Class != nil {
		t.Fatal(reset)
	}
	for _, body := range []map[string]any{
		{"class": "decision"}, {"class": ""}, {"class": "unknown"}, {"class": 12},
		{"prompt": strings.Repeat("x", 65537)}, {"tools": []string{"bash"}},
		{"name": "new"}, {"compaction": map[string]any{}},
	} {
		refused(t, request(t, a.Server, "PUT", "/api/agents/eika", body), 400)
	}
	refused(t, request(t, a.Server, "PUT", "/api/agents/eika", json.RawMessage(`null`)), 400)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		refused(t, request(t, a.Server, method, "/api/agents/browser", map[string]any{}), 404)
	}
	for _, path := range []string{"/api/agents", "/api/agents/eika"} {
		refused(t, requestWith(t, a.Server, "GET", path, nil, ""), 401)
	}
	refused(t, requestWith(t, a.Server, "PUT", "/api/agents/eika", map[string]any{"prompt": "attack"}, ""), 401)
	refused(t, requestWith(t, a.Server, "DELETE", "/api/agents/eika", nil, ""), 401)
	// Existing callers keep their profile routes and configuration.
	if got := a.profiles(t); len(got.Profiles) != 1 {
		t.Fatal(got)
	}
}
