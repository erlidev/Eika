//go:build docker

package server_test

import (
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
	// An override may not widen what an agent can do.
	for _, body := range []map[string]any{{"class": "decision"}, {"tools": []string{"bash"}}} {
		refused(t, request(t, a.Server, "PUT", "/api/agents/eika", body), 400)
	}
	refused(t, request(t, a.Server, "GET", "/api/agents/browser", nil), 404)
}
