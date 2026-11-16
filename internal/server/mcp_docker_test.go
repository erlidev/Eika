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
	Computers            []string `json:"computers"`
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
		Name          string `json:"name"`
		NeedsComputer bool   `json:"needs_computer"`
		Server        string `json:"server"`
	} `json:"tools"`
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
func (a *api) connectMCP(t *testing.T, id, computerID string) mcpDetailsWire {
	t.Helper()
	rec := request(t, a.Server, "POST", "/api/mcp/servers/"+id+"/connect", map[string]any{"computer_id": computerID})
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
		{"credentials in the url", map[string]any{"name": "cred", "kind": "http", "url": "https://u:p@mcp.example/mcp"}, 400},
		{"a header the client sets", map[string]any{"name": "hdr", "kind": "http", "url": docsURL, "headers": map[string]string{"Mcp-Session-Id": "x"}}, 400},
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
		"a query string": "http://example.com/mcp/callback?next=x",
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

func TestAStdioMCPServerRunsInTheComputer(t *testing.T) {
	a := newAPI(t)
	local := &mcptest.Server{Name: "files", Tools: []mcptest.Tool{echoTool("read")}}
	var spec sandbox.ProcessSpec
	a.host.process = func(s sandbox.ProcessSpec) (io.ReadWriteCloser, error) {
		spec = s
		return local.Pipe(), nil
	}
	server := a.addMCPServer(t, map[string]any{
		"name": "files", "kind": "stdio", "command": "npx", "args": []string{"-y", "files"},
		"env": map[string]string{"ROOT": "/home/eika"},
	})
	cx := a.home(t)

	refused(t, request(t, a.Server, "POST", "/api/mcp/servers/"+server.ID+"/connect", map[string]any{}), 400)
	d := a.connectMCP(t, server.ID, cx.ID)
	if d.Server.State != "connected" || !slices.Equal(d.Server.Computers, []string{cx.ID}) || d.Connection == nil || d.Connection.Transport != "stdio" {
		t.Fatalf("details = %+v", d)
	}
	if spec.Command != "npx" || !slices.Equal(spec.Args, []string{"-y", "files"}) || !slices.Equal(spec.Env, []string{"ROOT=/home/eika"}) {
		t.Errorf("process = %+v", spec)
	}
	if got := a.host.started(); !slices.Equal(got, []string{cx.ID}) {
		t.Errorf("started in %v", got)
	}

	// A computer session's run offers the tool; it needs the computer.
	sess := a.newSession(t, cx.ID)
	tools := decodeBody[mcpToolsWire](t, request(t, a.Server, "GET", "/api/tools", nil), 200)
	for _, tl := range tools.Tools {
		if tl.Name == "mcp__files__read" && (!tl.NeedsComputer || tl.Server != "files") {
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
	chat := a.newChat(t, "no computer")
	if slices.Contains(chat.Tools, "mcp__files__read") {
		t.Errorf("chat tools = %v", chat.Tools)
	}

	// Stopping the computer ends the server there.
	a.stopIdle(t)
	d = a.mcpDetails(t, server.ID)
	if len(d.Server.Computers) != 0 || d.Server.State == "connected" {
		t.Errorf("after the computer stopped = %+v", d.Server)
	}
	// A stopped computer starts no server.
	rec := request(t, a.Server, "POST", "/api/mcp/servers/"+server.ID+"/connect", map[string]any{"computer_id": cx.ID})
	if _, message := errorOf(t, rec); rec.Code != 409 || !strings.Contains(message, "not running") {
		t.Errorf("connect in a stopped computer = %d %q", rec.Code, message)
	}
	if got := a.host.started(); len(got) != 1 {
		t.Errorf("started in %v, want no second process", got)
	}
}
