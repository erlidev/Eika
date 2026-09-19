//go:build docker

package server_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// signInWire is the body of a successful setup, sign-in, or password change.
type signInWire struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type authStatusWire struct {
	PasswordSet bool `json:"password_set"`
}

type providerWire struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	BaseURL    string `json:"base_url"`
	APIKeySet  bool   `json:"api_key_set"`
	APIKeyHint string `json:"api_key_hint"`
}

type providersWire struct {
	Providers []providerWire `json:"providers"`
	Kinds     []string       `json:"kinds"`
}

type probeWire struct {
	Models []provider.ModelInfo `json:"models"`
}

type systemWire struct {
	Docker struct {
		Reachable bool   `json:"reachable"`
		Error     string `json:"error"`
	} `json:"docker"`
	SandboxImage struct {
		Name    string `json:"name"`
		Present bool   `json:"present"`
	} `json:"sandbox_image"`
	Providers int `json:"providers"`
	Models    int `json:"models"`
	Projects  int `json:"projects"`
}

// listingProvider is a provider whose endpoint can list its models.
type listingProvider struct {
	*providertest.Provider
	models []provider.ModelInfo
	err    error
}

func (p listingProvider) Models(context.Context) ([]provider.ModelInfo, error) {
	return p.models, p.err
}

// bearer is an Authorization header for a token.
func bearer(token string) string { return "Bearer " + token }

func TestSetupSignInAndSignOut(t *testing.T) {
	a := newAPI(t)
	const password = "correct horse battery"

	status := decodeBody[authStatusWire](t, requestWith(t, a.Server, "GET", "/api/auth/status", nil, ""), 200)
	if status.PasswordSet {
		t.Fatal("a new harness reports a password")
	}
	if rec := requestWith(t, a.Server, "POST", "/api/auth/login", map[string]any{"password": password}, ""); rec.Code != 409 {
		t.Errorf("sign-in before setup = %d, want 409", rec.Code)
	}
	if rec := requestWith(t, a.Server, "POST", "/api/auth/setup", map[string]any{"password": "short"}, ""); rec.Code != 400 {
		t.Errorf("setup with a short password = %d, want 400", rec.Code)
	}

	first := decodeBody[signInWire](t, requestWith(t, a.Server, "POST", "/api/auth/setup", map[string]any{"password": password}, ""), 201)
	if first.Token == "" || first.ExpiresAt == "" {
		t.Fatalf("setup = %+v", first)
	}
	if rec := requestWith(t, a.Server, "GET", "/api/projects", nil, bearer(first.Token)); rec.Code != 200 {
		t.Errorf("the setup token = %d, want 200", rec.Code)
	}
	// Setup is claimed once; a second browser signs in instead.
	if rec := requestWith(t, a.Server, "POST", "/api/auth/setup", map[string]any{"password": "another password"}, ""); rec.Code != 409 {
		t.Errorf("a second setup = %d, want 409", rec.Code)
	}
	status = decodeBody[authStatusWire](t, requestWith(t, a.Server, "GET", "/api/auth/status", nil, ""), 200)
	if !status.PasswordSet {
		t.Error("the harness does not report its password after setup")
	}

	if rec := requestWith(t, a.Server, "POST", "/api/auth/login", map[string]any{"password": "wrong password"}, ""); rec.Code != 401 {
		t.Errorf("a wrong password = %d, want 401", rec.Code)
	}
	second := decodeBody[signInWire](t, requestWith(t, a.Server, "POST", "/api/auth/login", map[string]any{"password": password}, ""), 200)

	// Signing out ends that session and no other.
	if rec := requestWith(t, a.Server, "POST", "/api/auth/logout", nil, bearer(first.Token)); rec.Code != 204 {
		t.Fatalf("sign-out = %d, want 204", rec.Code)
	}
	if rec := requestWith(t, a.Server, "GET", "/api/projects", nil, bearer(first.Token)); rec.Code != 401 {
		t.Errorf("a signed-out token = %d, want 401", rec.Code)
	}
	if rec := requestWith(t, a.Server, "GET", "/api/projects", nil, bearer(second.Token)); rec.Code != 200 {
		t.Errorf("the other session = %d, want 200", rec.Code)
	}

	// A new password ends every session and signs this browser in again.
	if rec := requestWith(t, a.Server, "PUT", "/api/auth/password", map[string]any{
		"current_password": "wrong password", "new_password": "a new password",
	}, bearer(second.Token)); rec.Code != 400 {
		t.Errorf("a change with the wrong current password = %d, want 400", rec.Code)
	}
	third := decodeBody[signInWire](t, requestWith(t, a.Server, "PUT", "/api/auth/password", map[string]any{
		"current_password": password, "new_password": "a new password",
	}, bearer(second.Token)), 200)
	if rec := requestWith(t, a.Server, "GET", "/api/projects", nil, bearer(second.Token)); rec.Code != 401 {
		t.Errorf("a session from before the change = %d, want 401", rec.Code)
	}
	if rec := requestWith(t, a.Server, "GET", "/api/projects", nil, bearer(third.Token)); rec.Code != 200 {
		t.Errorf("the session the change made = %d, want 200", rec.Code)
	}
	if rec := requestWith(t, a.Server, "POST", "/api/auth/login", map[string]any{"password": password}, ""); rec.Code != 401 {
		t.Errorf("the old password = %d, want 401", rec.Code)
	}

	// The API token is not a session and works throughout.
	if rec := request(t, a.Server, "GET", "/api/projects", nil); rec.Code != 200 {
		t.Errorf("the API token = %d, want 200", rec.Code)
	}
	// The event stream takes a session token in the query, as it takes the
	// API token.
	if rec := requestWith(t, a.Server, "GET", "/api/events?token="+third.Token, nil, ""); rec.Code == 401 {
		t.Error("the event stream refused a session token")
	}
}

func TestProviderRoutes(t *testing.T) {
	a := newAPI(t)
	const key = "sk-test-0123456789abcdef"

	rec := request(t, a.Server, "POST", "/api/providers", map[string]any{
		"name": "OpenAI", "base_url": "https://api.openai.com/v1", "api_key": key,
	})
	created := decodeBody[providerWire](t, rec, 201)
	if created.Kind != "openai" || !created.APIKeySet || created.APIKeyHint != "cdef" {
		t.Errorf("created = %+v", created)
	}
	if strings.Contains(rec.Body.String(), key) {
		t.Errorf("the response carries the key: %s", rec.Body.String())
	}
	stored, err := a.store.Provider(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("read provider: %v", err)
	}
	if strings.Contains(string(stored.APIKey), key) {
		t.Error("the stored key is not sealed")
	}

	for name, body := range map[string]map[string]any{
		"a taken name":         {"name": "OpenAI", "base_url": "https://x.test/v1"},
		"no name":              {"name": " ", "base_url": "https://x.test/v1"},
		"no base url":          {"name": "x"},
		"a base url with ftp":  {"name": "x", "base_url": "ftp://x.test"},
		"credentials in a url": {"name": "x", "base_url": "https://u:p@x.test/v1"},
		"a query in a url":     {"name": "x", "base_url": "https://x.test/v1?key=1"},
		"an unknown kind":      {"name": "x", "kind": "carrier-pigeon", "base_url": "https://x.test/v1"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := request(t, a.Server, "POST", "/api/providers", body)
			if rec.Code != 400 && rec.Code != 409 {
				t.Errorf("status = %d, want a rejection: %s", rec.Code, rec.Body.String())
			}
		})
	}

	list := decodeBody[providersWire](t, request(t, a.Server, "GET", "/api/providers", nil), 200)
	if len(list.Providers) != 2 || len(list.Kinds) != 1 || list.Kinds[0] != "openai" {
		t.Errorf("list = %+v", list)
	}

	updated := decodeBody[providerWire](t, request(t, a.Server, "PATCH", "/api/providers/"+created.ID,
		map[string]any{"name": "OpenAI personal", "api_key": ""}), 200)
	if updated.Name != "OpenAI personal" || updated.APIKeySet || updated.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("updated = %+v", updated)
	}

	model := decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{
		"provider_id": created.ID, "model": "gpt-5", "context_window": 400000, "max_output": 128000,
	}), 201)
	if rec := request(t, a.Server, "DELETE", "/api/providers/"+created.ID, nil); rec.Code != 204 {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec := request(t, a.Server, "PATCH", "/api/models/"+model.ID, map[string]any{"max_output": 1}); rec.Code != 404 {
		t.Errorf("a model of a deleted provider = %d, want 404", rec.Code)
	}
}

func TestProbeListsWhatTheEndpointServes(t *testing.T) {
	a := newAPI(t)
	served := []provider.ModelInfo{{ID: "alpha", ContextWindow: 128000}, {ID: "beta"}}
	a.use(listingProvider{models: served})

	got := decodeBody[probeWire](t, request(t, a.Server, "POST", "/api/providers/probe", map[string]any{
		"base_url": "https://new.test/v1", "api_key": "typed-in",
	}), 200)
	if len(got.Models) != 2 || got.Models[0] != served[0] {
		t.Errorf("models = %+v", got.Models)
	}
	if e := a.lastEndpoint(); e.BaseURL != "https://new.test/v1" || e.APIKey != "typed-in" {
		t.Errorf("endpoint = %+v, want the one in the request", e)
	}

	// A stored provider is probed with its stored key unless the form has a
	// new one.
	decodeBody[probeWire](t, request(t, a.Server, "POST", "/api/providers/probe", map[string]any{
		"provider_id": a.testProvider.ID,
	}), 200)
	if e := a.lastEndpoint(); e.BaseURL != "http://model.invalid" || e.APIKey != "test-key" {
		t.Errorf("endpoint = %+v, want the stored provider's", e)
	}
	decodeBody[probeWire](t, request(t, a.Server, "POST", "/api/providers/probe", map[string]any{
		"provider_id": a.testProvider.ID, "api_key": "replacement",
	}), 200)
	if e := a.lastEndpoint(); e.APIKey != "replacement" {
		t.Errorf("endpoint key = %q, want the form's", e.APIKey)
	}

	// What the endpoint says comes back, without the key it may quote.
	a.use(listingProvider{err: &provider.Error{Op: "list models", StatusCode: 401, Err: errors.New("bad key sk-leaky")}})
	rec := request(t, a.Server, "POST", "/api/providers/probe", map[string]any{
		"base_url": "https://new.test/v1", "api_key": "sk-leaky",
	})
	if rec.Code != 400 || strings.Contains(rec.Body.String(), "sk-leaky") || !strings.Contains(rec.Body.String(), "bad key") {
		t.Errorf("refused key = %d %s", rec.Code, rec.Body.String())
	}

	// A local endpoint the container cannot reach gets the Docker hint.
	a.use(listingProvider{err: &provider.Error{Op: "list models", Err: errors.New("connection refused")}})
	rec = request(t, a.Server, "POST", "/api/providers/probe", map[string]any{"base_url": "http://localhost:11434/v1"})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "host.docker.internal") {
		t.Errorf("unreachable local endpoint = %d %s, want the host.docker.internal hint", rec.Code, rec.Body.String())
	}

	a.script()
	if rec := request(t, a.Server, "POST", "/api/providers/probe", map[string]any{"base_url": "https://new.test/v1"}); rec.Code != 400 {
		t.Errorf("a provider that cannot list = %d, want 400", rec.Code)
	}
}

func TestModelRoutes(t *testing.T) {
	a := newAPI(t)
	created := decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{
		"provider_id": a.testProvider.ID, "model": "gpt-x", "context_window": 1000, "max_output": 100,
		"reasoning_effort": "high", "preserve_thinking": true,
	}), 201)
	if created.Name != "gpt-x" || created.ReasoningEffort != "high" || !created.PreserveThinking {
		t.Errorf("created = %+v, want the name to default to the model", created)
	}

	for name, body := range map[string]map[string]any{
		"a taken name":           {"provider_id": a.testProvider.ID, "model": "gpt-x", "context_window": 10, "max_output": 1},
		"no model":               {"provider_id": a.testProvider.ID, "context_window": 10, "max_output": 1},
		"output past the window": {"provider_id": a.testProvider.ID, "model": "m", "context_window": 10, "max_output": 11},
		"no window":              {"provider_id": a.testProvider.ID, "model": "m", "max_output": 1},
		"an unknown effort":      {"provider_id": a.testProvider.ID, "model": "m", "context_window": 10, "max_output": 1, "reasoning_effort": "extreme"},
		"a provider that is not": {"provider_id": "absent", "model": "m", "context_window": 10, "max_output": 1},
	} {
		t.Run(name, func(t *testing.T) {
			rec := request(t, a.Server, "POST", "/api/models", body)
			if rec.Code != 400 && rec.Code != 409 {
				t.Errorf("status = %d, want a rejection: %s", rec.Code, rec.Body.String())
			}
		})
	}

	// Renaming the default keeps it the default.
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"default_model": "gpt-x"}), 200)
	decodeBody[modelWire](t, request(t, a.Server, "PATCH", "/api/models/"+created.ID, map[string]any{"name": "fast"}), 200)
	models := decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200)
	if models.Default != "fast" {
		t.Errorf("default after the rename = %q, want fast", models.Default)
	}

	// Deleting the default falls back to the first model.
	if rec := request(t, a.Server, "DELETE", "/api/models/"+created.ID, nil); rec.Code != 204 {
		t.Fatalf("delete = %d", rec.Code)
	}
	models = decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200)
	if len(models.Models) != 1 || models.Default != "test-model" {
		t.Errorf("models after the delete = %+v", models)
	}
}

func TestModelTestSendsOneSmallRequest(t *testing.T) {
	a := newAPI(t)
	p := a.script(providertest.Text("ready"))
	got := decodeBody[struct {
		Reply      string `json:"reply"`
		StopReason string `json:"stop_reason"`
	}](t, request(t, a.Server, "POST", "/api/models/test", map[string]any{
		"provider_id": a.testProvider.ID, "model": "unsaved-model", "reasoning_effort": "low",
	}), 200)
	if got.Reply != "ready" || got.StopReason != "stop" {
		t.Errorf("test = %+v", got)
	}
	reqs := p.Requests()
	if len(reqs) != 1 || reqs[0].Model != "unsaved-model" || reqs[0].ReasoningEffort != "low" || len(reqs[0].Tools) != 0 {
		t.Errorf("requests = %+v", reqs)
	}

	a.script(providertest.Fail(&provider.Error{Op: "stream", StatusCode: 404, Err: errors.New("model not found")}))
	rec := request(t, a.Server, "POST", "/api/models/test", map[string]any{"provider_id": a.testProvider.ID, "model": "typo"})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "model not found") {
		t.Errorf("a failing model = %d %s", rec.Code, rec.Body.String())
	}
}

func TestARunUsesTheModelsEndpointIdentifierAndLimits(t *testing.T) {
	a := newAPI(t)
	second := decodeBody[providerWire](t, request(t, a.Server, "POST", "/api/providers", map[string]any{
		"name": "second", "base_url": "https://second.test/v1", "api_key": "second-key",
	}), 201)
	decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{
		"provider_id": second.ID, "name": "friendly", "model": "vendor/model-7b",
		"context_window": 32768, "max_output": 777, "reasoning_effort": "medium",
	}), 201)
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"default_model": "friendly"}), 200)

	p := a.script(providertest.Text("done"))
	sess := a.session(t)
	a.postMessage(t, sess.ID, "hello", "", 202)
	a.waitIdle(t, sess.ID)

	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Model != "vendor/model-7b" || reqs[0].MaxTokens != 777 || reqs[0].ReasoningEffort != "medium" {
		t.Errorf("request = model %q, max %d, effort %q", reqs[0].Model, reqs[0].MaxTokens, reqs[0].ReasoningEffort)
	}
	if e := a.lastEndpoint(); e.BaseURL != "https://second.test/v1" || e.APIKey != "second-key" {
		t.Errorf("endpoint = %+v, want the default model's provider", e)
	}
}

func TestNoModelMeansNoRun(t *testing.T) {
	a := newAPI(t)
	if rec := request(t, a.Server, "DELETE", "/api/providers/"+a.testProvider.ID, nil); rec.Code != 204 {
		t.Fatalf("delete provider = %d", rec.Code)
	}
	sess := a.session(t)
	rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages", map[string]any{"text": "hi"})
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "no model") {
		t.Errorf("a run with no model = %d %s, want 409", rec.Code, rec.Body.String())
	}
}

func TestSystemReport(t *testing.T) {
	a := newAPI(t)
	a.host.images = map[string]bool{"eika-sandbox:latest": true}
	got := decodeBody[systemWire](t, request(t, a.Server, "GET", "/api/system", nil), 200)
	if !got.Docker.Reachable || got.SandboxImage.Name != "eika-sandbox:latest" || !got.SandboxImage.Present {
		t.Errorf("system = %+v", got)
	}
	if got.Providers != 1 || got.Models != 1 || got.Projects != 0 {
		t.Errorf("counts = %d providers, %d models, %d projects", got.Providers, got.Models, got.Projects)
	}

	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"sandbox_image": "custom:2"}), 200)
	got = decodeBody[systemWire](t, request(t, a.Server, "GET", "/api/system", nil), 200)
	if got.SandboxImage.Name != "custom:2" || got.SandboxImage.Present {
		t.Errorf("image = %+v, want the setting's, missing", got.SandboxImage)
	}

	// A new workspace runs the image the settings name.
	project, _ := a.newProject(t, "demo")
	if ws := a.newWorkspace(t, project.ID); ws.Image != "custom:2" {
		t.Errorf("workspace image = %q, want the setting's", ws.Image)
	}

	a.host.imageErr = fmt.Errorf("permission denied while trying to connect to the docker daemon socket")
	got = decodeBody[systemWire](t, request(t, a.Server, "GET", "/api/system", nil), 200)
	if got.Docker.Reachable || !strings.Contains(got.Docker.Error, "permission denied") {
		t.Errorf("docker = %+v, want the daemon's reason", got.Docker)
	}
}
