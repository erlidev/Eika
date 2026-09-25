//go:build docker

package server_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/mcp/mcptest"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// The wire shapes of the profile and context routes.
type configurationWire struct {
	ProfileID       string            `json:"profile_id"`
	ProfileName     string            `json:"profile_name"`
	ModelID         string            `json:"model_id"`
	Model           string            `json:"model"`
	WorkspacePrompt string            `json:"workspace_prompt"`
	ChatPrompt      string            `json:"chat_prompt"`
	Instructions    string            `json:"instructions"`
	ContextFiles    bool              `json:"context_files"`
	Tools           []string          `json:"tools"`
	Sampling        provider.Sampling `json:"sampling"`
	DroppedEffort   string            `json:"dropped_effort"`
	Sources         map[string]string `json:"sources"`
}

type profileWire struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	ModelID         string            `json:"model_id"`
	WorkspacePrompt *string           `json:"workspace_prompt"`
	ChatPrompt      *string           `json:"chat_prompt"`
	Instructions    *string           `json:"instructions"`
	ContextFiles    *bool             `json:"context_files"`
	Sampling        provider.Sampling `json:"sampling"`
	Tools           []string          `json:"tools"`
	Inherited       configurationWire `json:"inherited"`
}

type profilesWire struct {
	Profiles []profileWire `json:"profiles"`
	Default  string        `json:"default"`
	Prompts  struct {
		Workspace string `json:"workspace"`
		Chat      string `json:"chat"`
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

type modelRequestWire struct {
	ID            string `json:"id"`
	RunID         string `json:"run_id"`
	EntryID       string `json:"entry_id"`
	ModelID       string `json:"model_id"`
	Model         string `json:"model"`
	MessageTokens int    `json:"message_tokens"`
	InputTokens   int    `json:"input_tokens"`
}

type requestsWire struct {
	SessionID string             `json:"session_id"`
	Requests  []modelRequestWire `json:"requests"`
}

type contextWire struct {
	agent.Context
	Sources       map[string]string `json:"sources"`
	DroppedEffort string            `json:"dropped_effort"`
	Unread        string            `json:"context_files_unread"`
	Request       *modelRequestWire `json:"request"`
	Calibration   *struct {
		RequestID       string `json:"request_id"`
		InputTokens     int    `json:"input_tokens"`
		EstimatedTokens int    `json:"estimated_tokens"`
	} `json:"calibration"`
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

// configuration reads what a session sets and resolves to.
func (a *api) configuration(t *testing.T, sessionID string) sessionConfigurationWire {
	t.Helper()
	rec := request(t, a.Server, "GET", "/api/sessions/"+sessionID+"/configuration", nil)
	return decodeBody[sessionConfigurationWire](t, rec, 200)
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
	if listed.Prompts.Workspace != agent.WorkspacePrompt || listed.Prompts.Chat != agent.ChatPrompt {
		t.Errorf("prompts = %+v, want the built-in ones", listed.Prompts)
	}
	original := listed.Profiles[0]
	if original.WorkspacePrompt != nil || original.Tools != nil || original.Inherited.Model != "test-model" ||
		deref(original.Inherited.Sampling.MaxOutput) != 1024 || original.Inherited.Sources["sampling.max_output"] != "model" {
		t.Errorf("default profile = %+v, want one that sets nothing and inherits the model row", original)
	}

	careful := a.newProfile(t, map[string]any{
		"name": " Careful ", "description": "reviews", "instructions": "Check twice.",
		"workspace_prompt": "", "context_files": false,
		"sampling": map[string]any{"temperature": 0.1, "stop": []string{"END"}},
		"tools":    []string{"bash", "ask_user", "bash"},
	})
	if careful.Name != "Careful" || deref(careful.Instructions) != "Check twice." || careful.WorkspacePrompt == nil ||
		*careful.WorkspacePrompt != "" || deref(careful.ContextFiles) || deref(careful.Sampling.Temperature) != 0.1 {
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
		{"a name that differs in case", map[string]any{"name": "careful"}, 201},
		{"the same name", map[string]any{"name": "Careful"}, 409},
		{"no name", map[string]any{"name": " "}, 400},
		{"a sampling parameter out of range", map[string]any{"name": "hot", "sampling": map[string]any{"temperature": 3}}, 400},
		{"a model that does not exist", map[string]any{"name": "m", "model_id": "missing"}, 400},
		{"a tool that does not exist", map[string]any{"name": "t", "tools": []string{"teleport"}}, 400},
		{"an MCP server that does not exist", map[string]any{"name": "s", "tools": []string{"mcp__nowhere__*"}}, 400},
		{"an unknown field", map[string]any{"name": "u", "temperature": 1}, 400},
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
	project, dir := a.newProject(t, "demo")
	write(t, dir, "AGENTS.md", "Use tabs.")
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)

	profile := a.newProfile(t, map[string]any{
		"name": "Terse", "workspace_prompt": "You are terse.", "instructions": "Answer in one line.",
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
	if !slices.Equal(kinds, []agent.SectionKind{agent.SectionBase, agent.SectionContextFiles, agent.SectionInstructions}) {
		t.Errorf("sections = %v", kinds)
	}
	if preview.Sources["sampling.temperature"] != "session" || preview.Sources["model"] != "default" || preview.Calibration != nil {
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
	if !strings.HasPrefix(sent.System, "You are terse.") || !strings.Contains(sent.System, "Use tabs.") {
		t.Errorf("system prompt = %q, want the profile's prompt and the context file", sent.System)
	}
	if deref(sent.Sampling.Temperature) != 0.9 || deref(sent.Sampling.MaxOutput) != 200 {
		t.Errorf("sampling = %+v", sent.Sampling)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Name != "bash" {
		t.Errorf("tools = %+v, want the profile's choice", sent.Tools)
	}

	// The call is recorded and reads back in the preview's shape.
	records := decodeBody[requestsWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/requests", nil), 200)
	if len(records.Requests) != 1 || records.Requests[0].Model != "test-model" || records.Requests[0].EntryID == "" {
		t.Fatalf("requests = %+v, want the one call", records)
	}
	rec := records.Requests[0]
	recorded := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/requests/"+rec.ID, nil), 200)
	if recorded.System() != sent.System || len(recorded.Tools) != 1 || recorded.Request == nil || recorded.Request.ID != rec.ID {
		t.Errorf("recorded = %+v", recorded)
	}
	if len(recorded.Messages) != 1 || recorded.Messages[0].Content != "hello" {
		t.Errorf("recorded messages = %+v, want the conversation the call sent", recorded.Messages)
	}
	if recorded.Sources["sampling.temperature"] != "session" || deref(recorded.Parameters.Sampling.Temperature) != 0.9 {
		t.Errorf("recorded parameters = %+v, sources %v", recorded.Parameters, recorded.Sources)
	}
	refused(t, request(t, a.Server, "GET", "/api/sessions/"+a.newSession(t, ws.ID).ID+"/requests/"+rec.ID, nil), 404)

	// A request for a model names the top layer.
	named := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/context?model=test-model", nil), 200)
	if named.Sources["model"] != "request" {
		t.Errorf("sources = %v, want the model from the request", named.Sources)
	}
	refused(t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/context?model=typo", nil), 400)
}

func TestAnEffortTheModelDoesNotOfferIsNotSent(t *testing.T) {
	a := newAPI(t)
	models := decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200)
	decodeBody[modelWire](t, request(t, a.Server, "PATCH", "/api/models/"+models.Models[0].ID, map[string]any{
		"reasoning_effort": "low", "reasoning_efforts": []string{"low", "high"},
	}), 200)
	chat := a.newChat(t, "think")
	config := a.override(t, chat.ID, map[string]any{"sampling": map[string]any{"reasoning_effort": "max"}})
	if config.Resolved.DroppedEffort != "max" || deref(config.Resolved.Sampling.ReasoningEffort) != "low" {
		t.Errorf("resolved = %+v, want max dropped for the model's own", config.Resolved)
	}
	p := a.script(providertest.Text("done"))
	a.postMessage(t, chat.ID, "hi", "", 202)
	a.waitIdle(t, chat.ID)
	if got := deref(p.Requests()[0].Sampling.ReasoningEffort); got != "low" {
		t.Errorf("sent effort = %q, want the model's own", got)
	}
}

func TestAToolChoiceNamesAnMCPServer(t *testing.T) {
	a := newAPI(t)
	docs := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo"), echoTool("shout")}}
	a.addMCPServer(t, map[string]any{"name": "docs", "kind": "http", "url": a.serveMCP(t, docs)})
	chat := a.newChat(t, "docs")
	profile := a.newProfile(t, map[string]any{"name": "Docs", "tools": []string{"mcp__docs__*", "web_search"}})
	a.useProfile(t, chat.ID, profile.ID)

	p := a.script(providertest.Text("done"))
	a.postMessage(t, chat.ID, "hi", "", 202)
	a.waitIdle(t, chat.ID)
	var offered []string
	for _, d := range p.Requests()[0].Tools {
		offered = append(offered, d.Name)
	}
	slices.Sort(offered)
	if want := []string{"mcp__docs__echo", "mcp__docs__shout", "web_search"}; !slices.Equal(offered, want) {
		t.Errorf("offered = %v, want %v", offered, want)
	}

	// A session's own choice may name a server too.
	put := request(t, a.Server, "PUT", "/api/sessions/"+chat.ID+"/tools", map[string]any{"tools": []string{"mcp__docs__*"}})
	got := decodeBody[profiledSessionWire](t, put, 200)
	if !slices.Equal(got.Tools, []string{"mcp__docs__echo", "mcp__docs__shout"}) {
		t.Errorf("session tools = %v, want every tool of docs", got.Tools)
	}
	// Null clears the session's choice, and the profile's applies again.
	put = request(t, a.Server, "PUT", "/api/sessions/"+chat.ID+"/tools", map[string]any{"tools": nil})
	got = decodeBody[profiledSessionWire](t, put, 200)
	if !slices.Equal(got.Tools, []string{"mcp__docs__echo", "mcp__docs__shout", "web_search"}) || got.Overridden {
		t.Errorf("session tools = %v, overridden %v; want the profile's again", got.Tools, got.Overridden)
	}
}

func TestForksAndChildrenKeepTheProfile(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)
	profile := a.newProfile(t, map[string]any{"name": "Inherited"})
	a.useProfile(t, sess.ID, profile.ID)
	a.override(t, sess.ID, map[string]any{"instructions": "from the parent"})

	a.script(
		spawnStep("c1", "worker", "write the notes", true),
		providertest.Text("nothing to do"),
		providertest.Text("the child is done"),
	)
	a.postMessage(t, sess.ID, "hand it to a child", "", 202)
	a.waitIdle(t, sess.ID)
	child := a.waitAgent(t, sess.ID)

	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	fork := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/fork", map[string]any{
		"entry_id": path.Entries[len(path.Entries)-1].ID,
	}), 201)

	for name, id := range map[string]string{"child": child.SessionID, "fork": fork.ID} {
		got := a.configuration(t, id)
		if got.ProfileID != profile.ID || deref(got.Overrides.Instructions) != "from the parent" {
			t.Errorf("%s configuration = %+v, want the parent's profile and overrides", name, got)
		}
	}
}

func TestAnEditorSeesTheValuesOfTheModelItChoseBeforeSaving(t *testing.T) {
	a := newAPI(t)
	models := decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200)
	big := decodeBody[modelWire](t, request(t, a.Server, "POST", "/api/models", map[string]any{
		"provider_id": models.Models[0].ProviderID, "name": "big", "model": "big", "context_window": 128000, "max_output": 32000,
	}), 201)

	profile := decodeBody[configurationWire](t, request(t, a.Server, "GET", "/api/profiles/inherited?model_id="+big.ID, nil), 200)
	if deref(profile.Sampling.MaxOutput) != 32000 || profile.Sources["sampling.max_output"] != "model" {
		t.Errorf("a profile choosing big inherits %+v, want big's max output", profile)
	}
	none := decodeBody[configurationWire](t, request(t, a.Server, "GET", "/api/profiles/inherited", nil), 200)
	if deref(none.Sampling.MaxOutput) == 32000 {
		t.Errorf("a profile choosing no model inherits %+v, want the default model's values", none)
	}
	if rec := request(t, a.Server, "GET", "/api/profiles/inherited?model_id=absent", nil); rec.Code != 400 {
		t.Errorf("an unknown model = %d, want 400", rec.Code)
	}

	chat := a.newChat(t, "pick")
	rec := request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/configuration?model_id="+big.ID, nil)
	config := decodeBody[sessionConfigurationWire](t, rec, 200)
	if deref(config.Inherited.Sampling.MaxOutput) != 32000 || config.Overrides.ModelID != "" {
		t.Errorf("a session choosing big = %+v, want big's max output inherited and nothing saved", config)
	}
	if saved := a.configuration(t, chat.ID); deref(saved.Inherited.Sampling.MaxOutput) == 32000 {
		t.Errorf("the saved configuration = %+v, want the default model's values", saved.Inherited)
	}
	if rec := request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/configuration?model_id=absent", nil); rec.Code != 400 {
		t.Errorf("an unknown model = %d, want 400", rec.Code)
	}

	terse := a.newProfile(t, map[string]any{"name": "Terse", "instructions": "One line."})
	rec = request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/configuration?profile_id="+terse.ID, nil)
	config = decodeBody[sessionConfigurationWire](t, rec, 200)
	if config.Inherited.Instructions != "One line." || config.Inherited.ProfileID != terse.ID || config.ProfileID != "" {
		t.Errorf("a session choosing Terse = %+v, want Terse's values inherited and nothing saved", config)
	}
	if rec := request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/configuration?profile_id=absent", nil); rec.Code != 400 {
		t.Errorf("an unknown profile = %d, want 400", rec.Code)
	}
}

func TestAStoppedWorkspaceIsPreviewedWithItsContextFilesUnread(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	write(t, dir, "AGENTS.md", "Use tabs.")
	ws := a.newWorkspace(t, project.ID)
	sess := a.newSession(t, ws.ID)
	if rec := request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/stop", nil); rec.Code != 200 {
		t.Fatalf("stop = %d", rec.Code)
	}

	preview := decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/context", nil), 200)
	if preview.Unread == "" {
		t.Error("the preview does not say the context files were not read")
	}
	if len(preview.Sections) != 1 || preview.Sections[0].Kind != agent.SectionBase || preview.Sections[0].Text != agent.WorkspacePrompt {
		t.Errorf("sections = %+v, want the workspace's base prompt alone", preview.Sections)
	}

	// A session that reads no context files misses nothing.
	a.override(t, sess.ID, map[string]any{"context_files": false})
	preview = decodeBody[contextWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/context", nil), 200)
	if preview.Unread != "" {
		t.Errorf("unread = %q with context files off, want none", preview.Unread)
	}
}
