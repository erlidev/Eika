//go:build docker

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor/sandbox"
	"github.com/erlidev/eika/internal/mcp/mcptest"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// The MCP wire shapes the tests decode, written out as the other wire types
// here are.
type mcpServerWire struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Kind                 string   `json:"kind"`
	URL                  string   `json:"url"`
	HeaderNames          []string `json:"header_names"`
	Command              string   `json:"command"`
	Args                 []string `json:"args"`
	EnvNames             []string `json:"env_names"`
	Enabled              bool     `json:"enabled"`
	DisabledTools        []string `json:"disabled_tools"`
	OAuthClientID        string   `json:"oauth_client_id"`
	OAuthClientSecretSet bool     `json:"oauth_client_secret_set"`
	State                string   `json:"state"`
	Error                string   `json:"error"`
	Workspaces           []string `json:"workspaces"`
}

type mcpServersWire struct {
	Servers []mcpServerWire `json:"servers"`
}

type mcpDetailsWire struct {
	Server     mcpServerWire `json:"server"`
	Connection *struct {
		Era             string `json:"era"`
		Transport       string `json:"transport"`
		ProtocolVersion string `json:"protocol_version"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"server_info"`
		Capabilities struct {
			Tools     bool `json:"tools"`
			Resources bool `json:"resources"`
			Prompts   bool `json:"prompts"`
		} `json:"capabilities"`
		Instructions string `json:"instructions"`
	} `json:"connection"`
	Tools []struct {
		Name        string          `json:"name"`
		ExposedName string          `json:"exposed_name"`
		InputSchema json.RawMessage `json:"input_schema"`
		Enabled     bool            `json:"enabled"`
	} `json:"tools"`
	Resources []struct {
		URI  string `json:"uri"`
		Name string `json:"name"`
	} `json:"resources"`
	Prompts []struct {
		Name      string `json:"name"`
		Arguments []struct {
			Name     string `json:"name"`
			Required bool   `json:"required"`
		} `json:"arguments"`
	} `json:"prompts"`
	Logs []mcpLogWire `json:"logs"`
	Auth struct {
		Challenged      bool   `json:"challenged"`
		Authorized      bool   `json:"authorized"`
		HasRefreshToken bool   `json:"has_refresh_token"`
		Issuer          string `json:"issuer"`
		ClientID        string `json:"client_id"`
		Registration    string `json:"registration"`
	} `json:"auth"`
}

type mcpLogWire struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}

type mcpToolsWire struct {
	Tools []struct {
		Name           string `json:"name"`
		NeedsWorkspace bool   `json:"needs_workspace"`
		Server         string `json:"server"`
	} `json:"tools"`
}

type elicitationWire struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	CallID    string `json:"call_id"`
	Server    string `json:"server"`
	Mode      string `json:"mode"`
	Message   string `json:"message"`
}

type elicitationStateWire struct {
	Active       bool              `json:"active"`
	Elicitations []elicitationWire `json:"elicitations"`
}

// serveMCP serves an MCP server over HTTP at /mcp and returns its URL.
func (a *api) serveMCP(t *testing.T, s http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(s)
	a.closesBefore(t, srv)
	return srv.URL + "/mcp"
}

// closesBefore closes the pool before srv, so that no connection to it is
// open when it waits for its connections to end.
func (a *api) closesBefore(t *testing.T, srv *httptest.Server) {
	t.Cleanup(srv.Close)
	t.Cleanup(a.mcp.Close)
}

// echoTool answers with the arguments it was called with.
func echoTool(name string) mcptest.Tool {
	return mcptest.Tool{
		Definition: mcptest.ToolDef(name, "Echo the arguments.", `{"type":"object","properties":{"text":{"type":"string"}}}`),
		Call: func(_ context.Context, c *mcptest.Call) (any, *mcptest.Error) {
			return mcptest.Text("echo " + string(c.Arguments)), nil
		},
	}
}

// addMCPServer creates a server and returns it as the API reports it.
func (a *api) addMCPServer(t *testing.T, body map[string]any) mcpServerWire {
	t.Helper()
	return decodeBody[mcpServerWire](t, request(t, a.Server, "POST", "/api/mcp/servers", body), 201)
}

// mcpDetails reads everything the API knows of a server.
func (a *api) mcpDetails(t *testing.T, id string) mcpDetailsWire {
	t.Helper()
	return decodeBody[mcpDetailsWire](t, request(t, a.Server, "GET", "/api/mcp/servers/"+id, nil), 200)
}

// connectMCP connects a server again and reports how it went.
func (a *api) connectMCP(t *testing.T, id, workspaceID string) mcpDetailsWire {
	t.Helper()
	rec := request(t, a.Server, "POST", "/api/mcp/servers/"+id+"/connect", map[string]any{"workspace_id": workspaceID})
	return decodeBody[mcpDetailsWire](t, rec, 200)
}

func TestMCPServerRoutes(t *testing.T) {
	a := newAPI(t)
	docs := &mcptest.Server{Name: "docs", Tools: []mcptest.Tool{echoTool("echo")}}
	docsURL := a.serveMCP(t, docs)
	events := a.Bus().Subscribe(event.TopicGlobal)
	defer events.Close()

	created := a.addMCPServer(t, map[string]any{
		"name": "docs", "kind": "http", "url": docsURL,
		"headers":         map[string]string{"X-Api-Key": "header-secret"},
		"oauth_client_id": "client-1", "oauth_client_secret": "client-secret",
	})
	if created.ID == "" || !created.Enabled || !slices.Equal(created.HeaderNames, []string{"X-Api-Key"}) ||
		!created.OAuthClientSecretSet || created.OAuthClientID != "client-1" {
		t.Errorf("created = %+v", created)
	}

	// Secrets are sealed in the row and never on the wire.
	row, err := a.store.MCPServer(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row.Headers), "header-secret") || strings.Contains(string(row.OAuthClientSecret), "client-secret") {
		t.Error("a secret is stored in the clear")
	}
	for _, path := range []string{"/api/mcp/servers", "/api/mcp/servers/" + created.ID} {
		if body := request(t, a.Server, "GET", path, nil).Body.String(); strings.Contains(body, "header-secret") || strings.Contains(body, "client-secret") {
			t.Errorf("GET %s returns a secret: %s", path, body)
		}
	}

	// A remote server connects as soon as it is added, with its headers.
	waitFor(t, "the server to connect", func() bool {
		return a.mcpDetails(t, created.ID).Server.State == "connected"
	})
	if reqs := docs.Requests(); len(reqs) == 0 || reqs[0].Headers.Get("X-Api-Key") != "header-secret" {
		t.Errorf("the server was not sent its header: %+v", reqs)
	}
	waitFor(t, "an mcp.server event", func() bool {
		for {
			select {
			case e := <-events.Events():
				var p event.MCPServer
				if e.Type == event.TypeMCPServer && json.Unmarshal(e.Payload, &p) == nil && p.ServerID == created.ID && p.State == "connected" {
					return true
				}
			default:
				return false
			}
		}
	})

	cases := []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"a taken name", map[string]any{"name": "docs", "kind": "http", "url": docsURL}, 409},
		{"a name with a space", map[string]any{"name": "my docs", "kind": "http", "url": docsURL}, 400},
		{"a name with a double underscore", map[string]any{"name": "a__b", "kind": "http", "url": docsURL}, 400},
		{"an unknown kind", map[string]any{"name": "ws", "kind": "websocket", "url": "wss://x"}, 400},
		{"an http server without a url", map[string]any{"name": "nourl", "kind": "http"}, 400},
		{"credentials in the url", map[string]any{"name": "cred", "kind": "http", "url": "https://u:p@mcp.example/mcp"}, 400},
		{"a header the client sets", map[string]any{"name": "hdr", "kind": "http", "url": docsURL, "headers": map[string]string{"Mcp-Session-Id": "x"}}, 400},
		{"a header value on two lines", map[string]any{"name": "hdr", "kind": "http", "url": docsURL, "headers": map[string]string{"X-A": "a\nb"}}, 400},
		{"an http server with a command", map[string]any{"name": "mixed", "kind": "http", "url": docsURL, "command": "npx"}, 400},
		{"a stdio server without a command", map[string]any{"name": "local", "kind": "stdio"}, 400},
		{"a stdio server with headers", map[string]any{"name": "local", "kind": "stdio", "command": "npx", "headers": map[string]string{"X-A": "b"}}, 400},
		{"a bad environment name", map[string]any{"name": "local", "kind": "stdio", "command": "npx", "env": map[string]string{"1X": "b"}}, 400},
		{"a client secret without a client", map[string]any{"name": "sec", "kind": "http", "url": docsURL, "oauth_client_secret": "s"}, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refused(t, request(t, a.Server, "POST", "/api/mcp/servers", c.body), c.status)
		})
	}

	local := a.addMCPServer(t, map[string]any{
		"name": "files", "kind": "stdio", "command": "npx", "args": []string{"-y", "server"},
		"env": map[string]string{"TOKEN": "env-secret", "MODE": "ro"}, "enabled": false,
	})
	if local.Enabled || local.State != "disabled" || !slices.Equal(local.EnvNames, []string{"MODE", "TOKEN"}) || !slices.Equal(local.Args, []string{"-y", "server"}) {
		t.Errorf("stdio server = %+v", local)
	}
	list := decodeBody[mcpServersWire](t, request(t, a.Server, "GET", "/api/mcp/servers", nil), 200)
	if len(list.Servers) != 2 || list.Servers[0].Name != "docs" || list.Servers[1].Name != "files" {
		t.Errorf("servers = %+v", list.Servers)
	}

	// A null value keeps the value stored under its name.
	patched := decodeBody[mcpServerWire](t, request(t, a.Server, "PATCH", "/api/mcp/servers/"+local.ID, map[string]any{
		"name": "fs", "env": map[string]any{"TOKEN": nil, "EXTRA": "1"}, "disabled_tools": []string{"write", "write", " "},
	}), 200)
	if patched.Name != "fs" || !slices.Equal(patched.EnvNames, []string{"EXTRA", "TOKEN"}) || !slices.Equal(patched.DisabledTools, []string{"write"}) {
		t.Errorf("patched = %+v", patched)
	}
	cfg, err := a.mcp.Details(t.Context(), local.ID)
	if err != nil || cfg.State != "disabled" {
		t.Errorf("details after the patch = %+v, %v", cfg, err)
	}
	refused(t, request(t, a.Server, "PATCH", "/api/mcp/servers/"+local.ID, map[string]any{"env": map[string]any{"MISSING": nil}}), 400)
	refused(t, request(t, a.Server, "PATCH", "/api/mcp/servers/"+local.ID, map[string]any{"url": docsURL}), 400)
	refused(t, request(t, a.Server, "PATCH", "/api/mcp/servers/"+local.ID, map[string]any{"name": "docs"}), 409)

	// Moving a server to another URL drops the headers given for the old one.
	other := a.serveMCP(t, &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}})
	moved := decodeBody[mcpServerWire](t, request(t, a.Server, "PATCH", "/api/mcp/servers/"+created.ID, map[string]any{"url": other}), 200)
	if moved.URL != other || len(moved.HeaderNames) != 0 || !moved.OAuthClientSecretSet {
		t.Errorf("moved = %+v, want the headers dropped and the client kept", moved)
	}
	cleared := decodeBody[mcpServerWire](t, request(t, a.Server, "PATCH", "/api/mcp/servers/"+created.ID, map[string]any{"oauth_client_id": ""}), 200)
	if cleared.OAuthClientID != "" || cleared.OAuthClientSecretSet {
		t.Errorf("cleared = %+v, want the secret gone with its client", cleared)
	}

	if rec := request(t, a.Server, "DELETE", "/api/mcp/servers/"+local.ID, nil); rec.Code != 204 {
		t.Fatalf("delete = %d: %s", rec.Code, rec.Body.String())
	}
	refused(t, request(t, a.Server, "GET", "/api/mcp/servers/"+local.ID, nil), 404)
	refused(t, request(t, a.Server, "DELETE", "/api/mcp/servers/"+local.ID, nil), 404)
	waitFor(t, "a removed event", func() bool {
		for {
			select {
			case e := <-events.Events():
				var p event.MCPServer
				if e.Type == event.TypeMCPServer && json.Unmarshal(e.Payload, &p) == nil && p.ServerID == local.ID && p.State == "removed" {
					return true
				}
			default:
				return false
			}
		}
	})
}

func TestMCPServerDetailsAndTools(t *testing.T) {
	a := newAPI(t)
	docs := &mcptest.Server{
		Name:         "docs",
		Instructions: "Search before you read.",
		Tools:        []mcptest.Tool{echoTool("echo"), echoTool("drop")},
		Resources:    []json.RawMessage{json.RawMessage(`{"uri":"docs://readme","name":"readme","mimeType":"text/markdown"}`)},
		ReadResource: func(uri string) (any, *mcptest.Error) {
			return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": "text/markdown", "text": "# Readme"}}}, nil
		},
		Prompts: []json.RawMessage{json.RawMessage(`{"name":"review","arguments":[{"name":"file","required":true}]}`)},
		GetPrompt: func(name string, args map[string]string) (any, *mcptest.Error) {
			return map[string]any{"description": "A review", "messages": []map[string]any{
				{"role": "user", "content": map[string]any{"type": "text", "text": "Review " + args["file"]}},
			}}, nil
		},
	}
	srv := a.addMCPServer(t, map[string]any{"name": "docs", "kind": "http", "url": a.serveMCP(t, docs)})

	d := a.connectMCP(t, srv.ID, "")
	if d.Server.State != "connected" || d.Connection == nil {
		t.Fatalf("details = %+v", d)
	}
	if c := d.Connection; c.Era != "modern" || c.Transport != "streamable_http" || c.ServerInfo.Name != "docs" ||
		!c.Capabilities.Tools || !c.Capabilities.Resources || !c.Capabilities.Prompts || c.Instructions != "Search before you read." {
		t.Errorf("connection = %+v", c)
	}
	if len(d.Tools) != 2 || d.Tools[0].ExposedName != "mcp__docs__echo" || !d.Tools[0].Enabled || len(d.Tools[0].InputSchema) == 0 {
		t.Errorf("tools = %+v", d.Tools)
	}
	if len(d.Resources) != 1 || d.Resources[0].URI != "docs://readme" || len(d.Prompts) != 1 || !d.Prompts[0].Arguments[0].Required {
		t.Errorf("resources = %+v, prompts = %+v", d.Resources, d.Prompts)
	}
	if !slices.ContainsFunc(d.Logs, func(l mcpLogWire) bool { return l.Source == "eika" && strings.HasPrefix(l.Text, "connected") }) {
		t.Errorf("logs = %+v, want the connection recorded", d.Logs)
	}

	// The tool list names the server of each MCP tool; a remote server's
	// tools need no workspace.
	tools := decodeBody[mcpToolsWire](t, request(t, a.Server, "GET", "/api/tools", nil), 200)
	servers := map[string]string{}
	for _, tl := range tools.Tools {
		servers[tl.Name] = tl.Server
		if strings.HasPrefix(tl.Name, "mcp_") && tl.NeedsWorkspace {
			t.Errorf("%s needs a workspace", tl.Name)
		}
	}
	if servers["mcp__docs__echo"] != "docs" || servers["mcp__docs__drop"] != "docs" || servers["bash"] != "" {
		t.Errorf("tool servers = %v", servers)
	}
	if _, ok := servers["mcp_read_resource"]; !ok {
		t.Errorf("tools = %v, want the resource tools for a server with resources", servers)
	}

	res := decodeBody[struct {
		Contents []struct {
			Type string `json:"type"`
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"contents"`
	}](t, request(t, a.Server, "POST", "/api/mcp/servers/"+srv.ID+"/resources/read", map[string]any{"uri": "docs://readme"}), 200)
	if len(res.Contents) != 1 || res.Contents[0].Text != "# Readme" || res.Contents[0].URI != "docs://readme" {
		t.Errorf("resource = %+v", res)
	}
	refused(t, request(t, a.Server, "POST", "/api/mcp/servers/"+srv.ID+"/resources/read", map[string]any{"uri": ""}), 400)

	prompt := decodeBody[struct {
		Description string `json:"description"`
		Messages    []struct {
			Role    string `json:"role"`
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}](t, request(t, a.Server, "POST", "/api/mcp/servers/"+srv.ID+"/prompts/get", map[string]any{"name": "review", "arguments": map[string]string{"file": "main.go"}}), 200)
	if prompt.Description != "A review" || len(prompt.Messages) != 1 || prompt.Messages[0].Content.Text != "Review main.go" {
		t.Errorf("prompt = %+v", prompt)
	}

	// A tool the user turns off leaves the tool list and the session's
	// choice, and the details say so.
	request(t, a.Server, "PATCH", "/api/mcp/servers/"+srv.ID, map[string]any{"disabled_tools": []string{"drop"}})
	d = a.connectMCP(t, srv.ID, "")
	if d.Tools[1].Name != "drop" || d.Tools[1].Enabled {
		t.Errorf("tools after turning one off = %+v", d.Tools)
	}
	chat := a.newChat(t, "docs")
	if !slices.Contains(chat.Tools, "mcp__docs__echo") || slices.Contains(chat.Tools, "mcp__docs__drop") {
		t.Errorf("chat tools = %v", chat.Tools)
	}
	picked := decodeBody[chatWire](t, request(t, a.Server, "PUT", "/api/sessions/"+chat.ID+"/tools", map[string]any{"tools": []string{"mcp__docs__echo"}}), 200)
	if !slices.Equal(picked.Tools, []string{"mcp__docs__echo"}) {
		t.Errorf("picked = %v", picked.Tools)
	}
	refused(t, request(t, a.Server, "PUT", "/api/sessions/"+chat.ID+"/tools", map[string]any{"tools": []string{"mcp__docs__drop"}}), 400)

	// A server that is off refuses to connect.
	request(t, a.Server, "PATCH", "/api/mcp/servers/"+srv.ID, map[string]any{"enabled": false})
	refused(t, request(t, a.Server, "POST", "/api/mcp/servers/"+srv.ID+"/connect", map[string]any{}), 409)
	refused(t, request(t, a.Server, "POST", "/api/mcp/servers/missing/connect", map[string]any{}), 404)
}

func TestARunCallsAnMCPTool(t *testing.T) {
	a := newAPI(t)
	docs := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	a.addMCPServer(t, map[string]any{"name": "docs", "kind": "http", "url": a.serveMCP(t, docs)})
	chat := a.newChat(t, "docs")
	events := a.Bus().Subscribe(event.SessionTopic(chat.ID))
	defer events.Close()

	p := a.script(
		providertest.Calls("", providertest.Call("c1", "mcp__docs__echo", map[string]any{"text": "hi"})),
		providertest.Text("done"),
	)
	a.postMessage(t, chat.ID, "echo hi", "", 202)
	if final := a.waitIdle(t, chat.ID); final.Run == nil || final.Run.State != "done" {
		t.Fatalf("run = %+v", final.Run)
	}
	if !slices.ContainsFunc(p.Requests()[0].Tools, func(d provider.ToolDef) bool { return d.Name == "mcp__docs__echo" }) {
		t.Errorf("the model was not offered the MCP tool")
	}
	if results := toolResults(p.Requests()[1]); len(results) != 1 || results[0] != `echo {"text":"hi"}` {
		t.Errorf("tool results = %q", results)
	}
	var details struct {
		Server  string `json:"server"`
		Tool    string `json:"tool"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	for len(events.Events()) > 0 {
		e := <-events.Events()
		if e.Type != event.TypeToolResult {
			continue
		}
		var r event.ToolResult
		if err := json.Unmarshal(e.Payload, &r); err != nil || json.Unmarshal(r.Details, &details) != nil {
			t.Fatalf("tool result = %s", e.Payload)
		}
	}
	if details.Server != "docs" || details.Tool != "echo" || len(details.Content) != 1 || details.Content[0].Type != "text" {
		t.Errorf("tool result details = %+v", details)
	}
}

func TestAnMCPServerAsksTheUserDuringARun(t *testing.T) {
	a := newAPI(t)
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	greet := mcptest.Tool{
		Definition: mcptest.ToolDef("greet", "Greet someone.", `{"type":"object"}`),
		Call: func(_ context.Context, c *mcptest.Call) (any, *mcptest.Error) {
			if c.InputResponses == nil {
				return map[string]any{
					"resultType":    "input_required",
					"inputRequests": map[string]any{"who": map[string]any{"method": "elicitation/create", "params": map[string]any{"message": "Who?", "requestedSchema": schema}}},
					"requestState":  "state-1",
				}, nil
			}
			return mcptest.Text("answer " + string(c.InputResponses["who"])), nil
		},
	}
	a.addMCPServer(t, map[string]any{"name": "people", "kind": "http", "url": a.serveMCP(t, &mcptest.Server{Tools: []mcptest.Tool{greet}})})
	chat := a.newChat(t, "greet")
	p := a.script(
		providertest.Calls("", providertest.Call("c1", "mcp__people__greet", map[string]any{})),
		providertest.Text("done"),
	)
	a.postMessage(t, chat.ID, "greet someone", "", 202)

	var asked elicitationWire
	waitFor(t, "an elicitation", func() bool {
		state := decodeBody[elicitationStateWire](t, request(t, a.Server, "GET", "/api/sessions/"+chat.ID+"/run", nil), 200)
		if len(state.Elicitations) == 0 {
			return false
		}
		asked = state.Elicitations[0]
		return true
	})
	if asked.SessionID != chat.ID || asked.CallID != "c1" || asked.Server != "people" || asked.Mode != "form" || asked.Message != "Who?" {
		t.Errorf("elicitation = %+v", asked)
	}
	answer := "/api/elicitations/" + asked.ID + "/answer"
	refused(t, request(t, a.Server, "POST", answer, map[string]any{"action": "accept", "content": map[string]any{"name": 7}}), 400)
	refused(t, request(t, a.Server, "POST", answer, map[string]any{"action": "maybe"}), 400)
	if rec := request(t, a.Server, "POST", answer, map[string]any{"action": "accept", "content": map[string]any{"name": "Ada"}}); rec.Code != 204 {
		t.Fatalf("answer = %d: %s", rec.Code, rec.Body.String())
	}
	refused(t, request(t, a.Server, "POST", answer, map[string]any{"action": "decline"}), 404)
	a.waitIdle(t, chat.ID)
	if results := toolResults(p.Requests()[1]); len(results) != 1 || !strings.Contains(results[0], `"Ada"`) {
		t.Errorf("tool results = %q", results)
	}
}

func TestAuthorizingAnMCPServer(t *testing.T) {
	a := newAPI(t)
	auth := mcptest.NewAuthorization(t)
	auth.IssParameter = true
	protected := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	mux := http.NewServeMux()
	mux.Handle("/mcp", protected)
	srv := httptest.NewServer(mux)
	a.closesBefore(t, srv)
	resource := srv.URL + "/mcp"
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp", auth.ProtectedResource(resource, "tools:read"))
	protected.Authorize = func(header string) bool { return auth.Valid(header, resource) }
	protected.Challenge = fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, srv.URL)

	server := a.addMCPServer(t, map[string]any{"name": "secure", "kind": "http", "url": resource})
	d := a.connectMCP(t, server.ID, "")
	if d.Server.State != "unauthorized" || !d.Auth.Challenged || d.Auth.Authorized {
		t.Fatalf("before authorizing = %+v", d)
	}

	authorize := "/api/mcp/servers/" + server.ID + "/authorize"
	for name, uri := range map[string]string{
		"another host":   "https://evil.example/mcp/callback",
		"another path":   "http://example.com/elsewhere",
		"a query string": "http://example.com/mcp/callback?next=x",
		"not a url":      "::",
	} {
		t.Run(name, func(t *testing.T) {
			refused(t, request(t, a.Server, "POST", authorize, map[string]any{"redirect_uri": uri}), 400)
		})
	}
	// The request's own host is where the UI is.
	redirect := "http://example.com/mcp/callback"
	begun := decodeBody[struct {
		AuthorizationURL string `json:"authorization_url"`
	}](t, request(t, a.Server, "POST", authorize, map[string]any{"redirect_uri": redirect}), 200)
	back, err := auth.Approve(t.Context(), begun.AuthorizationURL)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := back.Scheme + "://" + back.Host + back.Path; got != redirect {
		t.Errorf("sent back to %s, want %s", got, redirect)
	}
	q := back.Query()

	callback := func(iss string) *httptest.ResponseRecorder {
		return request(t, a.Server, "POST", "/api/mcp/oauth/callback", map[string]any{"state": q.Get("state"), "code": q.Get("code"), "iss": iss})
	}
	// The wrong issuer is refused before the code is redeemed, and the
	// authorization is spent either way.
	refused(t, callback("https://evil.example"), 400)
	refused(t, callback(q.Get("iss")), 404)

	begun = decodeBody[struct {
		AuthorizationURL string `json:"authorization_url"`
	}](t, request(t, a.Server, "POST", authorize, map[string]any{"redirect_uri": redirect}), 200)
	if back, err = auth.Approve(t.Context(), begun.AuthorizationURL); err != nil {
		t.Fatal(err)
	}
	q = back.Query()
	done := decodeBody[struct {
		ServerID string `json:"server_id"`
	}](t, callback(q.Get("iss")), 200)
	if done.ServerID != server.ID {
		t.Errorf("callback server = %s, want %s", done.ServerID, server.ID)
	}
	d = a.mcpDetails(t, server.ID)
	if d.Server.State != "connected" || !d.Auth.Authorized || d.Auth.Challenged || d.Auth.Registration != "dynamic" || d.Auth.Issuer != auth.Issuer {
		t.Errorf("after authorizing = %+v", d)
	}
	// The token is sealed in its row and never on the wire.
	creds, err := a.store.MCPCredentials(t.Context(), server.ID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.secrets.Open(creds.AccessToken)
	if err != nil || token == "" || strings.Contains(string(creds.AccessToken), token) {
		t.Errorf("stored access token = %q, opens to %q, %v", creds.AccessToken, token, err)
	}
	if body := request(t, a.Server, "GET", "/api/mcp/servers/"+server.ID, nil).Body.String(); strings.Contains(body, token) {
		t.Errorf("details carry the token: %s", body)
	}

	if rec := request(t, a.Server, "DELETE", "/api/mcp/servers/"+server.ID+"/authorization", nil); rec.Code != 204 {
		t.Fatalf("sign out = %d: %s", rec.Code, rec.Body.String())
	}
	if len(auth.Revoked()) == 0 {
		t.Error("signing out revoked nothing")
	}
	d = a.mcpDetails(t, server.ID)
	if d.Auth.Authorized || d.Auth.ClientID == "" {
		t.Errorf("after signing out = %+v, want no token and the client kept", d.Auth)
	}
}

func TestAStdioMCPServerRunsInTheWorkspace(t *testing.T) {
	a := newAPI(t)
	local := &mcptest.Server{Name: "files", Tools: []mcptest.Tool{echoTool("read")}}
	var spec sandbox.ProcessSpec
	a.host.process = func(s sandbox.ProcessSpec) (io.ReadWriteCloser, error) {
		spec = s
		return local.Pipe(), nil
	}
	server := a.addMCPServer(t, map[string]any{
		"name": "files", "kind": "stdio", "command": "npx", "args": []string{"-y", "files"},
		"env": map[string]string{"ROOT": "/workspace"},
	})
	project, _ := a.newProject(t, "demo")
	ws := a.newWorkspace(t, project.ID)

	refused(t, request(t, a.Server, "POST", "/api/mcp/servers/"+server.ID+"/connect", map[string]any{}), 400)
	d := a.connectMCP(t, server.ID, ws.ID)
	if d.Server.State != "connected" || !slices.Equal(d.Server.Workspaces, []string{ws.ID}) || d.Connection == nil || d.Connection.Transport != "stdio" {
		t.Fatalf("details = %+v", d)
	}
	if spec.Command != "npx" || !slices.Equal(spec.Args, []string{"-y", "files"}) || !slices.Equal(spec.Env, []string{"ROOT=/workspace"}) {
		t.Errorf("process = %+v", spec)
	}
	if got := a.host.started(); !slices.Equal(got, []string{ws.ID}) {
		t.Errorf("started in %v", got)
	}

	// A workspace session's run offers the tool; it needs the workspace.
	sess := a.newSession(t, ws.ID)
	tools := decodeBody[mcpToolsWire](t, request(t, a.Server, "GET", "/api/tools", nil), 200)
	for _, tl := range tools.Tools {
		if tl.Name == "mcp__files__read" && (!tl.NeedsWorkspace || tl.Server != "files") {
			t.Errorf("tool = %+v", tl)
		}
	}
	p := a.script(
		providertest.Calls("", providertest.Call("c1", "mcp__files__read", map[string]any{"text": "x"})),
		providertest.Text("done"),
	)
	a.postMessage(t, sess.ID, "read", "", 202)
	a.waitIdle(t, sess.ID)
	if results := toolResults(p.Requests()[1]); len(results) != 1 || results[0] != `echo {"text":"x"}` {
		t.Errorf("tool results = %q", results)
	}
	// A chat never offers it.
	chat := a.newChat(t, "no workspace")
	if slices.Contains(chat.Tools, "mcp__files__read") {
		t.Errorf("chat tools = %v", chat.Tools)
	}

	// Stopping the workspace ends the server there.
	decodeBody[workspaceWire](t, request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/stop", nil), 200)
	d = a.mcpDetails(t, server.ID)
	if len(d.Server.Workspaces) != 0 || d.Server.State == "connected" {
		t.Errorf("after the workspace stopped = %+v", d.Server)
	}
	// A stopped workspace starts no server.
	rec := request(t, a.Server, "POST", "/api/mcp/servers/"+server.ID+"/connect", map[string]any{"workspace_id": ws.ID})
	if _, message := errorOf(t, rec); rec.Code != 409 || !strings.Contains(message, "not running") {
		t.Errorf("connect in a stopped workspace = %d %q", rec.Code, message)
	}
	if got := a.host.started(); len(got) != 1 {
		t.Errorf("started in %v, want no second process", got)
	}
}

func TestMCPErrorsAreWordedForTheUser(t *testing.T) {
	a := newAPI(t)
	// An authorization server that offers no registration: the user has to
	// register a client by hand, and the message says with which redirect.
	auth := mcptest.NewAuthorization(t)
	auth.NoRegistration = true
	protected := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	mux := http.NewServeMux()
	mux.Handle("/mcp", protected)
	srv := httptest.NewServer(mux)
	a.closesBefore(t, srv)
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp", auth.ProtectedResource(srv.URL+"/mcp"))
	protected.Authorize = func(string) bool { return false }
	protected.Challenge = fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, srv.URL)
	server := a.addMCPServer(t, map[string]any{"name": "secure", "kind": "http", "url": srv.URL + "/mcp"})
	a.connectMCP(t, server.ID, "")

	rec := request(t, a.Server, "POST", "/api/mcp/servers/"+server.ID+"/authorize", map[string]any{"redirect_uri": "http://example.com/mcp/callback"})
	code, message := errorOf(t, rec)
	if rec.Code != 400 || code != "invalid_request" || !strings.Contains(message, "http://example.com/mcp/callback") {
		t.Errorf("authorize = %d %s %q, want the redirect to register named", rec.Code, code, message)
	}
	refused(t, request(t, a.Server, "POST", "/api/mcp/oauth/callback", map[string]any{"state": "unknown", "code": "c"}), 404)
	refused(t, request(t, a.Server, "POST", "/api/elicitations/el-missing/answer", map[string]any{"action": "decline"}), 404)
}
