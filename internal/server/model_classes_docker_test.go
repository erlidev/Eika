//go:build docker

package server_test

import (
	"testing"
)

// A decision model answers typed questions, not chat, so it is never offered
// where a conversation is held: not as the default, a profile's or a
// session's model, not in a run's request, not as a chat class, and it does
// not count as the model setup needs. A chat model never serves the decision
// class.
func TestADecisionModelIsNeverAChatModel(t *testing.T) {
	a := newAPI(t)
	p := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{
		"name": "decider", "base_url": "https://decider.test/v1",
	}), 201)
	decision := decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{
		"provider_id": p.ID, "kind": "decision", "name": "d3", "model": "vllm-sr/d3-flash",
		"context_window": 32768, "max_output": 1024,
	}), 201)
	if decision.Kind != "decision" {
		t.Fatalf("kind = %q, want decision", decision.Kind)
	}

	refused(t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"default_model": "d3"}), 400)
	refused(t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"model_classes": map[string]any{"main": "d3"},
	}), 400)
	sess := a.session(t)
	refused(t, request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages", map[string]any{"text": "hi", "model": "d3"}), 400)

	// It serves the decision class, which no chat model does.
	a.assignClasses(t, map[string]any{"decision": "d3"})
	refused(t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"model_classes": map[string]any{"decision": "test-model"},
	}), 400)

	// The default stays a chat model however old or new the decision model is.
	models := decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200)
	if models.Default != "test-model" || len(models.Models) != 2 {
		t.Errorf("default = %q of %d models, want test-model and both listed", models.Default, len(models.Models))
	}
	if system := decodeBody[systemWire](t, request(t, a.Server, "GET", "/api/system", nil), 200); system.Models != 1 {
		t.Errorf("system models = %d, want only the chat model counted", system.Models)
	}
}
