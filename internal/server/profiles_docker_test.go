//go:build docker

package server_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// The wire shapes of the profile and context routes.
type configurationWire struct {
	ProfileID     string            `json:"profile_id"`
	ProfileName   string            `json:"profile_name"`
	ModelID       string            `json:"model_id"`
	Model         string            `json:"model"`
	ChatPrompt    string            `json:"chat_prompt"`
	Instructions  string            `json:"instructions"`
	PreserveThink bool              `json:"preserve_thinking"`
	Tools         []string          `json:"tools"`
	Sampling      provider.Sampling `json:"sampling"`
	DroppedEffort string            `json:"dropped_effort"`
	Sources       map[string]string `json:"sources"`
}

type profileWire struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	ModelID       string            `json:"model_id"`
	ChatPrompt    *string           `json:"chat_prompt"`
	Instructions  *string           `json:"instructions"`
	PreserveThink *bool             `json:"preserve_thinking"`
	Sampling      provider.Sampling `json:"sampling"`
	Tools         []string          `json:"tools"`
	Inherited     configurationWire `json:"inherited"`
}

type profilesWire struct {
	Profiles []profileWire `json:"profiles"`
	Default  string        `json:"default"`
	Prompts  struct {
		Chat string `json:"chat"`
	} `json:"prompts"`
}

type sessionConfigurationWire struct {
	SessionID string `json:"session_id"`
	ProfileID string `json:"profile_id"`
	Overrides struct {
		ModelID      string            `json:"model_id"`
		Instructions *string           `json:"instructions"`
		Sampling     provider.Sampling `json:"sampling"`
	} `json:"overrides"`
	Tools     []string          `json:"tools"`
	Resolved  configurationWire `json:"resolved"`
	Inherited configurationWire `json:"inherited"`
}

type profiledSessionWire struct {
	ID         string   `json:"id"`
	ProfileID  string   `json:"profile_id"`
	Overridden bool     `json:"overridden"`
	Tools      []string `json:"tools"`
}

type modelCallWire struct {
	ID               string `json:"id"`
	RunID            string `json:"run_id"`
	EntryID          string `json:"entry_id"`
	ModelID          string `json:"model_id"`
	Model            string `json:"model"`
	MessageTokens    int    `json:"message_tokens"`
	PromptTokens     *int   `json:"prompt_tokens"`
	CompletionTokens *int   `json:"completion_tokens"`
	TotalTokens      *int   `json:"total_tokens"`
	CachedTokens     *int   `json:"cached_tokens"`
	PrefilledTokens  *int   `json:"prefilled_tokens"`
	ReasoningTokens  *int   `json:"reasoning_tokens"`
	ReasoningSource  string `json:"reasoning_source"`
	Drift            *int   `json:"drift"`
	Prediction       string `json:"prediction"`

	PredictedPromptTokens *int   `json:"predicted_prompt_tokens"`
	ContextWindow         *int   `json:"context_window"`
	Endpoint              string `json:"endpoint"`
	QueueMS               *int   `json:"queue_ms"`
	Slot                  *int   `json:"slot"`
	Priority              string `json:"priority"`
}

type callsWire struct {
	SessionID string          `json:"session_id"`
	Calls     []modelCallWire `json:"calls"`
}

type contextWire struct {
	agent.Context
	Sources       map[string]string `json:"sources"`
	DroppedEffort string            `json:"dropped_effort"`
	Call          *modelCallWire    `json:"call"`
	ContextWindow int               `json:"context_window"`
	Breakdown     *breakdownWire    `json:"breakdown"`
	Prediction    *struct {
		Tokens int    `json:"tokens"`
		Method string `json:"method"`
	} `json:"prediction"`
}

type breakdownWire struct {
	agent.Breakdown
	PromptTokens *int   `json:"prompt_tokens"`
	PromptSource string `json:"prompt_source"`
	Remainder    *int   `json:"remainder"`
}

// profiles lists the profiles.
func (a *api) profiles(t *testing.T) profilesWire {
	t.Helper()
	return decodeBody[profilesWire](t, request(t, a.Server, "GET", "/api/profiles", nil), 200)
}

// newProfile creates a profile from a request body.
func (a *api) newProfile(t *testing.T, body map[string]any) profileWire {
	t.Helper()
	return decodeBody[profileWire](t, request(t, a.Server, "POST", "/api/profiles", body), 201)
}

// useProfile chooses a session's profile.
func (a *api) useProfile(t *testing.T, sessionID, profileID string) sessionConfigurationWire {
	t.Helper()
	rec := request(t, a.Server, "PUT", "/api/sessions/"+sessionID+"/profile", map[string]any{"profile_id": profileID})
	return decodeBody[sessionConfigurationWire](t, rec, 200)
}

// override sets a session's overrides.
func (a *api) override(t *testing.T, sessionID string, body map[string]any) sessionConfigurationWire {
	t.Helper()
	rec := request(t, a.Server, "PUT", "/api/sessions/"+sessionID+"/overrides", body)
	return decodeBody[sessionConfigurationWire](t, rec, 200)
}

// deref returns what p points to, or the zero value for nil.
func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func TestProfileRoutes(t *testing.T) {
	a := newAPI(t)
	listed := a.profiles(t)
	if len(listed.Profiles) != 1 || listed.Profiles[0].Name != "Default" || listed.Default != listed.Profiles[0].ID {
		t.Fatalf("profiles = %+v, want the one the migration made, as the default", listed)
	}
	if listed.Prompts.Chat != agent.ChatPrompt {
		t.Errorf("prompts = %+v, want the built-in ones", listed.Prompts)
	}
	original := listed.Profiles[0]
	if original.ChatPrompt != nil || original.Tools != nil || original.Inherited.Model != "test-model" ||
		deref(original.Inherited.Sampling.MaxOutput) != 1024 || original.Inherited.Sources["sampling.max_output"] != "model" {
		t.Errorf("default profile = %+v, want one that sets nothing and inherits the model row", original)
	}

	careful := a.newProfile(t, map[string]any{
		"name": " Careful ", "description": "reviews", "instructions": "Check twice.",
		"chat_prompt": "",
		"sampling":    map[string]any{"temperature": 0.1, "stop": []string{"END"}},
		"tools":       []string{"bash", "ask_user", "bash"},
	})
	if careful.Name != "Careful" || deref(careful.Instructions) != "Check twice." || careful.ChatPrompt == nil ||
		*careful.ChatPrompt != "" || deref(careful.Sampling.Temperature) != 0.1 {
		t.Errorf("created = %+v", careful)
	}
	if !slices.Equal(careful.Tools, []string{"ask_user", "bash"}) {
		t.Errorf("tools = %v, want each once, sorted", careful.Tools)
	}

	cases := []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"the same name", map[string]any{"name": "Careful"}, 409},
		{"a tool that does not exist", map[string]any{"name": "t", "tools": []string{"teleport"}}, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refused(t, request(t, a.Server, "POST", "/api/profiles", c.body), c.status)
		})
	}

	updated := decodeBody[profileWire](t, request(t, a.Server, "PUT", "/api/profiles/"+careful.ID, map[string]any{
		"name": "Careful", "sampling": map[string]any{"top_k": 20},
	}), 200)
	if updated.Instructions != nil || updated.Tools != nil || deref(updated.Sampling.TopK) != 20 || updated.Sampling.Temperature != nil {
		t.Errorf("updated = %+v, want the body to replace the whole profile", updated)
	}
	refused(t, request(t, a.Server, "PUT", "/api/profiles/missing", map[string]any{"name": "x"}), 404)

	// The default setting names a profile by id; deleting the default falls
	// back to the first profile.
	refused(t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"default_profile": "missing"}), 400)
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"default_profile": careful.ID}), 200)
	if got := a.profiles(t).Default; got != careful.ID {
		t.Errorf("default = %s, want the one the setting names", got)
	}
	refused(t, request(t, a.Server, "DELETE", "/api/profiles/"+careful.ID, nil), 204)
	if got := a.profiles(t).Default; got != original.ID {
		t.Errorf("default after deleting it = %s, want the first profile", got)
	}

	for _, p := range a.profiles(t).Profiles[1:] {
		refused(t, request(t, a.Server, "DELETE", "/api/profiles/"+p.ID, nil), 204)
	}
	refused(t, request(t, a.Server, "DELETE", "/api/profiles/"+original.ID, nil), 409)
}

func TestARunUsesItsProfileAndTheSessionsOverrides(t *testing.T) {
	a := newAPI(t)
	cx := a.home(t)
	sess := a.newSession(t, cx.ID)

	profile := a.newProfile(t, map[string]any{
		"name": "Terse", "chat_prompt": "You are terse.", "instructions": "Answer in one line.",
		"sampling": map[string]any{"temperature": 0.4, "max_output": 200},
		"tools":    []string{"bash"},
	})
	config := a.useProfile(t, sess.ID, profile.ID)
	if config.ProfileID != profile.ID || config.Resolved.ProfileName != "Terse" || config.Resolved.Sources["profile"] != "session" {
		t.Errorf("configuration = %+v, want the chosen profile", config)
	}
	config = a.override(t, sess.ID, map[string]any{"sampling": map[string]any{"temperature": 0.9}})
	resolved := config.Resolved
	if deref(resolved.Sampling.Temperature) != 0.9 || resolved.Sources["sampling.temperature"] != "session" ||
		deref(resolved.Sampling.MaxOutput) != 200 || resolved.Sources["sampling.max_output"] != "profile" {
		t.Errorf("resolved = %+v, want the session over the profile over the model", resolved)
	}
	if deref(config.Inherited.Sampling.Temperature) != 0.4 || config.Inherited.Sources["sampling.temperature"] != "profile" {
		t.Errorf("inherited = %+v, want the profile's temperature", config.Inherited)
	}
	listed := decodeBody[struct {
		Session profiledSessionWire `json:"session"`
	}](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID, nil), 200)
	if listed.Session.ProfileID != profile.ID || !listed.Session.Overridden || !slices.Equal(listed.Session.Tools, []string{"bash"}) {
		t.Errorf("session = %+v, want its profile, marked overridden, with the profile's tools", listed.Session)
	}
	refused(t, request(t, a.Server, "PUT", "/api/sessions/"+sess.ID+"/profile", map[string]any{"profile_id": "missing"}), 400)
	refused(t, request(t, a.Server, "PUT", "/api/sessions/missing/profile", map[string]any{"profile_id": ""}), 404)
	refused(t, request(t, a.Server, "PUT", "/api/sessions/"+sess.ID+"/overrides",
		map[string]any{"sampling": map[string]any{"top_p": 2}}), 400)

	// The preview is the request the run then sends.
	preview := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/context", nil), 200)
	var kinds []agent.SectionKind
	for _, s := range preview.Sections {
		kinds = append(kinds, s.Kind)
	}
	if !slices.Equal(kinds, []agent.SectionKind{agent.SectionBase, agent.SectionInstructions}) {
		t.Errorf("sections = %v", kinds)
	}
	if preview.Sources["sampling.temperature"] != "session" || preview.Sources["model"] != "default" || preview.Breakdown == nil || preview.Prediction == nil {
		t.Errorf("preview = %+v", preview)
	}

	p := a.script(providertest.Text("done"))
	a.postMessage(t, sess.ID, "hello", "", 202)
	if state := a.waitIdle(t, sess.ID); state.Run == nil || state.Run.State != "done" {
		t.Fatalf("run = %+v", state.Run)
	}
	sent := p.Requests()[0]
	if sent.System != preview.System() {
		t.Errorf("system prompt = %q\nwant the preview's %q", sent.System, preview.System())
	}
	if !strings.HasPrefix(sent.System, "You are terse.") || !strings.Contains(sent.System, "Answer in one line.") {
		t.Errorf("system prompt = %q, want the profile's prompt and instructions", sent.System)
	}
	if deref(sent.Sampling.Temperature) != 0.9 || deref(sent.Sampling.MaxOutput) != 200 {
		t.Errorf("sampling = %+v", sent.Sampling)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Name != "bash" {
		t.Errorf("tools = %+v, want the profile's choice", sent.Tools)
	}

	// The call is recorded and reads back in the preview's shape.
	records := decodeBody[callsWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/calls", nil), 200)
	if len(records.Calls) != 1 || records.Calls[0].Model != "test-model" || records.Calls[0].EntryID == "" {
		t.Fatalf("requests = %+v, want the one call", records)
	}
	rec := records.Calls[0]
	if rec.Endpoint != "test" || rec.ContextWindow == nil || rec.QueueMS == nil || *rec.QueueMS < 0 ||
		rec.Slot != nil || rec.Priority != "interactive" {
		t.Errorf("call attribution = endpoint %q, window %v, queue_ms %v, slot %v, priority %q; want the provider's name, the model's window, the queue's wait, no slot (the provider is not llama.cpp) and interactive",
			rec.Endpoint, rec.ContextWindow, rec.QueueMS, rec.Slot, rec.Priority)
	}
	recorded := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/calls/"+rec.ID, nil), 200)
	if recorded.System() != sent.System || len(recorded.Tools) != 1 || recorded.Call == nil || recorded.Call.ID != rec.ID {
		t.Errorf("recorded = %+v", recorded)
	}
	if len(recorded.Messages) != 1 || recorded.Messages[0].Content != "hello" {
		t.Errorf("recorded messages = %+v, want the conversation the call sent", recorded.Messages)
	}
	if recorded.Sources["sampling.temperature"] != "session" || deref(recorded.Parameters.Sampling.Temperature) != 0.9 {
		t.Errorf("recorded parameters = %+v, sources %v", recorded.Parameters, recorded.Sources)
	}
	refused(t, request(t, a.Server, "GET", "/api/sessions/"+a.newSession(t, cx.ID).ID+"/calls/"+rec.ID, nil), 404)

	// A request for a model names the top layer.
	named := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/context?model=test-model", nil), 200)
	if named.Sources["model"] != "request" {
		t.Errorf("sources = %v, want the model from the request", named.Sources)
	}
	refused(t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/context?model=typo", nil), 400)
}
