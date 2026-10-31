//go:build docker

package server_test

import (
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/probe/probetest"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// startChat sends the first message of a new chat and waits for the
// run to end, which is when its title has been asked for if it will be.
func (a *api) startChat(t *testing.T) (chatID string) {
	t.Helper()
	chat := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions", map[string]any{"chat": true}), 201)
	a.script(providertest.Text("an answer"))
	// The run names its model, so that a title model that is the default is
	// not the run's.
	decodeBody[runWire](t, request(t, a.Server, "POST", "/api/sessions/"+chat.ID+"/messages",
		map[string]any{"text": "how does the scheduler pick a task?", "model": "test-model"}), 202)
	a.waitIdle(t, chat.ID)
	return chat.ID
}

// A missing fast model must not turn titles off: the class falls back to main,
// and main to the default model. Titles are skipped only when the fallback
// lands on the model the user gave the deep class and that model is a hosted
// API, whether it got there through main or the default; a fast model the user
// chose is used whatever it is.
func TestTitlesRunOnFastAndFallBackToMain(t *testing.T) {
	const titled = "Explain the scheduler"
	for name, c := range map[string]struct {
		hosted bool
		// def makes the titler the default model.
		def     bool
		classes func(titler string) map[string]any
		titled  bool
	}{
		"fast is assigned": {
			classes: func(titler string) map[string]any { return map[string]any{"fast": titler} },
			titled:  true,
		},
		"fast is unset and main is a local model": {
			classes: func(titler string) map[string]any { return map[string]any{"main": titler} },
			titled:  true,
		},
		"fast is unset and main is a hosted model that is not deep": {
			hosted: true,
			classes: func(titler string) map[string]any {
				return map[string]any{"main": titler, "deep": "test-model"}
			},
			titled: true,
		},
		"fast is unset and main is a hosted model": {
			hosted:  true,
			classes: func(titler string) map[string]any { return map[string]any{"main": titler} },
			titled:  true,
		},
		"fast is unset and main is a local deep model": {
			classes: func(titler string) map[string]any { return map[string]any{"main": titler, "deep": titler} },
			titled:  true,
		},
		"fast is unset and main is a hosted deep model": {
			hosted:  true,
			classes: func(titler string) map[string]any { return map[string]any{"main": titler, "deep": titler} },
		},
		"fast is a hosted deep model by choice": {
			hosted:  true,
			classes: func(titler string) map[string]any { return map[string]any{"fast": titler, "deep": titler} },
			titled:  true,
		},
		"no class is assigned and the default is a local model": {
			def:     true,
			classes: func(string) map[string]any { return map[string]any{} },
			titled:  true,
		},
		"only deep is assigned and the default is a local model": {
			def:     true,
			classes: func(string) map[string]any { return map[string]any{"deep": "test-model"} },
			titled:  true,
		},
		"no class is assigned and the default is a hosted model that is not deep": {
			hosted: true,
			def:    true,
			classes: func(string) map[string]any {
				return map[string]any{"deep": "test-model"}
			},
			titled: true,
		},
		"no class is assigned and the default is a hosted model": {
			hosted:  true,
			def:     true,
			classes: func(string) map[string]any { return map[string]any{} },
			titled:  true,
		},
		"main is unset and the default is a hosted deep model": {
			hosted:  true,
			def:     true,
			classes: func(titler string) map[string]any { return map[string]any{"deep": titler} },
		},
		"fast is a hosted deep model by choice beside a default": {
			hosted:  true,
			def:     true,
			classes: func(titler string) map[string]any { return map[string]any{"fast": titler, "deep": titler} },
			titled:  true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := newAPI(t)
			titler := providertest.New(providertest.Text(titled))
			backend := ""
			if c.hosted {
				backend = "openai"
			}
			model := a.addTitleModelOn(t, titler, backend)
			a.assignClasses(t, c.classes(model))
			if c.def {
				decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"default_model": model}), 200)
			}

			chat := a.startChat(t)
			if !c.titled {
				if title := a.sessionTitle(t, chat); title != "New chat" || titler.Calls() != 0 {
					t.Errorf("title = %q after %d title calls, want the placeholder and no call", title, titler.Calls())
				}
				return
			}
			waitFor(t, "the chat to be titled", func() bool { return a.sessionTitle(t, chat) == titled })
			if titler.Calls() != 1 {
				t.Errorf("title calls = %d, want 1", titler.Calls())
			}
		})
	}
}

// The settings say which model serves each class once the fallbacks are
// applied, and on which pool, so a client can tell which classes contend for
// one endpoint's slots without repeating the fallback rule.
func TestTheSettingsResolveEveryClass(t *testing.T) {
	a := newAPI(t)
	got := decodeBody[settingsWire](t, request(t, a.Server, "GET", "/api/settings", nil), 200)
	// Before any class is assigned the chat classes are served by the default
	// model, and titles are made by it.
	if len(got.Classes) != 4 || got.TitlesModel != "test-model" {
		t.Fatalf("classes = %+v, titles model %q; want four classes and titles on the default model", got.Classes, got.TitlesModel)
	}
	for _, c := range got.Classes {
		model, via := "test-model", "default"
		if c.Class == "decision" {
			model, via = "", ""
		}
		if c.Model != model || c.Via != via {
			t.Errorf("class %s = model %q via %q, want %q via %q", c.Class, c.Model, c.Via, model, via)
		}
	}

	got = decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"model_classes": map[string]any{"main": "test-model"},
	}), 200)
	want := map[string]struct{ model, via string }{
		"deep": {"test-model", "main"}, "main": {"test-model", ""}, "fast": {"test-model", "main"}, "decision": {"", ""},
	}
	for _, c := range got.Classes {
		if w := want[c.Class]; c.Model != w.model || c.Via != w.via {
			t.Errorf("class %s = model %q via %q, want %q via %q", c.Class, c.Model, c.Via, w.model, w.via)
		}
		if c.Model != "" && (c.ProviderID != a.testProvider.ID || c.Pool != "test") {
			t.Errorf("class %s = provider %q pool %q, want the test provider's endpoint and pool", c.Class, c.ProviderID, c.Pool)
		}
		if c.Kind != map[bool]string{true: "decision", false: "chat"}[c.Class == "decision"] {
			t.Errorf("class %s kind = %q", c.Class, c.Kind)
		}
	}
	if got.TitlesModel != "test-model" {
		t.Errorf("titles model = %q, want titles to follow fast onto main", got.TitlesModel)
	}
}

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
	for _, class := range []string{"deep", "main", "fast"} {
		refused(t, request(t, a.Server, "PUT", "/api/settings", map[string]any{
			"model_classes": map[string]any{class: "d3"},
		}), 400)
	}
	refused(t, request(t, a.Server, "POST", "/api/profiles", map[string]any{"name": "judge", "model_id": decision.ID}), 400)

	sess := a.session(t)
	refused(t, request(t, a.Server, "PUT", "/api/sessions/"+sess.ID+"/overrides", map[string]any{"model_id": decision.ID}), 400)
	refused(t, request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages", map[string]any{"text": "hi", "model": "d3"}), 400)
	refused(t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/configuration?model_id="+decision.ID, nil), 400)

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

// With no chat model at all, a decision model leaves the harness with no
// default model, as an empty list does.
func TestADecisionModelAloneLeavesNoDefaultModel(t *testing.T) {
	a := newAPI(t)
	for _, m := range decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200).Models {
		if rec := request(t, a.Server, "DELETE", "/api/models/"+m.ID, nil); rec.Code != 204 {
			t.Fatalf("delete %s = %d", m.Name, rec.Code)
		}
	}
	p := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{
		"name": "decider", "base_url": "https://decider.test/v1",
	}), 201)
	decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{
		"provider_id": p.ID, "kind": "decision", "name": "d3", "model": "d3-flash",
		"context_window": 32768, "max_output": 1024,
	}), 201)
	if models := decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200); models.Default != "" {
		t.Errorf("default = %q, want none", models.Default)
	}
	sess := a.session(t)
	refused(t, request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages", map[string]any{"text": "hi"}), 409)
}

// A decision model is added with a kind that cannot change, and without the
// settings of a chat model.
func TestAModelsKindIsChosenOnceAndChecked(t *testing.T) {
	a := newAPI(t)
	p := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{
		"name": "decider", "base_url": "https://decider.test/v1",
	}), 201)
	create := func(extra map[string]any) map[string]any {
		body := map[string]any{"provider_id": p.ID, "name": "d3", "model": "d3-flash", "context_window": 32768, "max_output": 1024}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}
	for name, extra := range map[string]map[string]any{
		"an unknown kind":                      {"kind": "embedding"},
		"a decision model with a reasoning":    {"kind": "decision", "reasoning_effort": "high"},
		"a decision model with efforts":        {"kind": "decision", "reasoning_efforts": []string{"none"}},
		"a decision model with images":         {"kind": "decision", "image_input": true},
		"a decision model that keeps thinking": {"kind": "decision", "preserve_thinking": true},
	} {
		t.Run(name, func(t *testing.T) {
			refused(t, request(t, a.Server, "POST", "/api/models", create(extra)), 400)
		})
	}
	chat := decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", create(nil)), 201)
	if chat.Kind != "chat" {
		t.Errorf("a model created with no kind = %q, want chat", chat.Kind)
	}
	refused(t, request(t, a.Server, "PATCH", "/api/models/"+chat.ID, map[string]any{"kind": "decision"}), 400)
	decision := decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", create(map[string]any{"kind": "decision", "name": "d3b"})), 201)
	refused(t, request(t, a.Server, "PATCH", "/api/models/"+decision.ID, map[string]any{"image_input": true}), 400)
}

// The model test sends a chat request. A decision model cannot answer one, so
// the test refuses it and sends nothing.
func TestAChatRequestDoesNotTestADecisionModel(t *testing.T) {
	a := newAPI(t)
	served := providertest.New(providertest.Text("ready"))
	baseURL := "https://decider.test/v1"
	a.serveAt(baseURL, served)
	p := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{
		"name": "decider", "base_url": baseURL,
	}), 201)

	rec := request(t, a.Server, "POST", "/api/models/test", map[string]any{
		"provider_id": p.ID, "model": "d3-flash", "kind": "decision",
	})
	refused(t, rec, 400)
	if !strings.Contains(rec.Body.String(), "decision model") || served.Calls() != 0 {
		t.Errorf("test of a decision model: %s after %d requests, want a refusal that says why and no request", rec.Body.String(), served.Calls())
	}
	decodeBody[struct {
		Reply string `json:"reply"`
	}](t, request(t, a.Server, "POST", "/api/models/test", map[string]any{
		"provider_id": p.ID, "model": "qwen",
	}), 200)
	if served.Calls() != 1 {
		t.Errorf("requests after testing a chat model = %d, want 1", served.Calls())
	}
}

// The Responses check sends chat requests, so it asks the provider's oldest
// chat model and never a decision model, whichever is older. A provider with
// nothing but a decision model is not asked.
func TestTheResponsesCheckAsksNoDecisionModel(t *testing.T) {
	a := newAPI(t)
	baseURL := probetest.LlamaCpp(t, "").URL + "/v1"
	served := listingProvider{Provider: providertest.New(conforming()...), models: []provider.ModelInfo{{ID: "qwen3.8-27b"}}}
	a.serveAt(baseURL, served)
	p := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{"name": "bench", "base_url": baseURL}), 201)
	add := func(body map[string]any) {
		body["provider_id"], body["context_window"], body["max_output"] = p.ID, 32768, 4096
		decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", body), 201)
	}

	add(map[string]any{"kind": "decision", "name": "d3", "model": "d3-flash"})
	got := probeProvider(t, a, p.ID)
	c := providerNamed(t, a, p.ID).Capabilities.Responses
	if got.Probe.API != "chat" || c == nil || c.Verdict != "skipped" || served.Calls() != 0 {
		t.Fatalf("probe %q, conformance %+v after %d requests; want chat, skipped, and no request to a decision model", got.Probe.API, c, served.Calls())
	}

	add(map[string]any{"name": "qwen", "model": "qwen3.8-27b"})
	got = probeProvider(t, a, p.ID)
	c = providerNamed(t, a, p.ID).Capabilities.Responses
	if got.Probe.API != "responses" || c == nil || c.Verdict != "passed" || c.Model != "qwen3.8-27b" {
		t.Errorf("probe %q, conformance %+v; want the chat model asked, though the decision model is older", got.Probe.API, c)
	}
}
