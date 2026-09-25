package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/mcp/mcptest"
	"github.com/erlidev/eika/internal/mcp/oauth"
	"github.com/erlidev/eika/internal/tool"
)

// memStore keeps servers and credentials in memory.
type memStore struct {
	mu      sync.Mutex
	servers []mcp.ServerConfig
	creds   map[string]mcp.Credentials
}

func (s *memStore) MCPServers(context.Context) ([]mcp.ServerConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.servers), nil
}

func (s *memStore) MCPServer(_ context.Context, id string) (mcp.ServerConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cfg := range s.servers {
		if cfg.ID == id {
			return cfg, nil
		}
	}
	return mcp.ServerConfig{}, fmt.Errorf("server %s not found", id)
}

func (s *memStore) MCPCredentials(_ context.Context, id string) (mcp.Credentials, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.creds[id]
	return c, ok, nil
}

func (s *memStore) SaveMCPCredentials(_ context.Context, id string, c mcp.Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creds == nil {
		s.creds = map[string]mcp.Credentials{}
	}
	c.UpdatedAt = time.Now().UTC()
	s.creds[id] = c
	return nil
}

func (s *memStore) update(id string, fn func(*mcp.ServerConfig)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.servers {
		if s.servers[i].ID == id {
			fn(&s.servers[i])
		}
	}
}

// launcher starts stdio servers as in-memory pipes, recording where.
type launcher struct {
	server *mcptest.Server
	mu     sync.Mutex
	pipes  map[string][]io.ReadWriteCloser
}

func (l *launcher) Launch(_ context.Context, workspace string, cmd mcp.Command, stderr func(string)) (io.ReadWriteCloser, error) {
	if workspace == "" {
		return nil, errors.New("no workspace")
	}
	stderr("starting " + cmd.Name)
	p := l.server.Pipe()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pipes == nil {
		l.pipes = map[string][]io.ReadWriteCloser{}
	}
	l.pipes[workspace] = append(l.pipes[workspace], p)
	return p, nil
}

func (l *launcher) launched(workspace string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pipes[workspace])
}

// recorder keeps the events emitted.
type recorder struct {
	mu     sync.Mutex
	events []event.Event
	notify chan event.Event
}

func newRecorder() *recorder { return &recorder{notify: make(chan event.Event, 64)} }

func (r *recorder) Emit(_ context.Context, e event.Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
	select {
	case r.notify <- e:
	default:
	}
}

func (r *recorder) waitFor(t *testing.T, typ string) event.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-r.notify:
			if e.Type == typ {
				return e
			}
		case <-timeout:
			t.Fatalf("no %s event", typ)
		}
	}
}

func newPool(t *testing.T, store *memStore, opts mcp.Options) *mcp.Pool {
	t.Helper()
	opts.Store = store
	opts.Client = eika
	opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	p := mcp.NewPool(opts)
	t.Cleanup(p.Close)
	return p
}

func toolNames(tools []tool.Tool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name())
	}
	return out
}

func find(tools []tool.Tool, name string) tool.Tool {
	for _, t := range tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

func TestToolNames(t *testing.T) {
	long := strings.Repeat("x", 80)
	got := mcp.ToolNames("docs", []mcp.Tool{{Name: "search"}, {Name: "get.page"}, {Name: "get_page"}, {Name: long}, {Name: "天気"}})
	if got[0] != "mcp__docs__search" || got[1] != "mcp__docs__get_page" {
		t.Errorf("names = %v", got)
	}
	if got[2] == got[1] || !strings.HasPrefix(got[2], "mcp__docs__get_page_") {
		t.Errorf("a clash kept the same name: %v", got)
	}
	if len(got[3]) != 64 {
		t.Errorf("long name is %d characters: %s", len(got[3]), got[3])
	}
	if got[4] != "mcp__docs____" {
		t.Errorf("non-ASCII name = %s", got[4])
	}
	if got := mcp.ToolNames("my docs", []mcp.Tool{{Name: "a"}}); got[0] != "mcp__my_docs__a" {
		t.Errorf("server name with a space = %s", got[0])
	}
}

func TestPoolOffersAndCallsRemoteTools(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo"), echoTool("hidden"), {
		Definition: mcptest.ToolDef("bad", "Bad headers.", `{"type":"object","properties":{"a":{"type":"object","x-mcp-header":"A"}}}`),
	}}}
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "docs", Kind: mcp.KindHTTP, URL: serve(t, s), Enabled: true, DisabledTools: []string{"hidden"}}}}
	rec := newRecorder()
	pool := newPool(t, store, mcp.Options{Emitter: rec})

	tools := pool.Tools(context.Background(), "")
	if got := toolNames(tools); !slices.Equal(got, []string{"mcp__docs__echo"}) {
		t.Fatalf("tools = %v", got)
	}
	if _, ok := tools[0].(tool.Standalone); !ok {
		t.Error("a remote server's tool is not standalone")
	}
	res, err := tools[0].Call(context.Background(), tool.CallContext{SessionID: "sess"}, json.RawMessage(`{"text":"hi"}`))
	if err != nil || res.IsError || res.Content != `echo {"text":"hi"}` {
		t.Fatalf("Call = %+v, %v", res, err)
	}
	var details mcp.ResultDetails
	if err := json.Unmarshal(res.Details, &details); err != nil || details.Server != "docs" || details.Tool != "echo" {
		t.Errorf("details = %s", res.Details)
	}

	d, err := pool.Details(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if d.State != mcp.StateConnected || len(d.Catalog.Tools) != 2 || len(d.Catalog.Excluded) != 1 || d.Catalog.Excluded[0].Name != "bad" {
		t.Errorf("details = %+v", d)
	}
	if got := toolNames(pool.Offered()); !slices.Equal(got, []string{"mcp__docs__echo"}) {
		t.Errorf("offered = %v", got)
	}
	var payload event.MCPServer
	e := rec.waitFor(t, event.TypeMCPServer)
	if err := json.Unmarshal(e.Payload, &payload); err != nil || payload.ServerID != "s1" || e.Topic != event.TopicGlobal {
		t.Errorf("event = %+v", e)
	}
}

func TestPoolLeavesOutWhatIsOffOrDown(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	store := &memStore{servers: []mcp.ServerConfig{
		{ID: "off", Name: "off", Kind: mcp.KindHTTP, URL: serve(t, &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}})},
		{ID: "down", Name: "down", Kind: mcp.KindHTTP, URL: down.URL, Enabled: true},
		{ID: "local", Name: "local", Kind: mcp.KindStdio, Command: "server", Enabled: true},
	}}
	pool := newPool(t, store, mcp.Options{})
	if got := pool.Tools(context.Background(), ""); len(got) != 0 {
		t.Errorf("tools = %v, want none", toolNames(got))
	}
	if st := pool.Status("down"); st.State != mcp.StateError || st.Error == "" {
		t.Errorf("down server status = %+v", st)
	}
	if st := pool.Status("off"); st.State != mcp.StateIdle && st.State != mcp.StateDisabled {
		t.Errorf("off server status = %+v", st)
	}
}

func TestPoolRunsStdioServersInTheWorkspace(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	l := &launcher{server: s}
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "local", Kind: mcp.KindStdio, Command: "npx", Args: []string{"server"}, Enabled: true}}}
	pool := newPool(t, store, mcp.Options{Launcher: l})

	if got := pool.Tools(context.Background(), ""); len(got) != 0 {
		t.Errorf("tools without a workspace = %v", toolNames(got))
	}
	tools := pool.Tools(context.Background(), "ws1")
	if len(tools) != 1 || l.launched("ws1") != 1 {
		t.Fatalf("tools = %v, launched %d", toolNames(tools), l.launched("ws1"))
	}
	if _, ok := tools[0].(tool.Standalone); ok {
		t.Error("a stdio server's tool claims to need no workspace")
	}
	res, err := tools[0].Call(context.Background(), tool.CallContext{WorkspaceID: "ws1"}, json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("Call = %+v, %v", res, err)
	}
	pool.Tools(context.Background(), "ws2")
	if st := pool.Status("s1"); !slices.Equal(st.Workspaces, []string{"ws1", "ws2"}) {
		t.Errorf("status = %+v", st)
	}
	d, _ := pool.Details(context.Background(), "s1")
	if !slices.ContainsFunc(d.Logs, func(l mcp.LogLine) bool { return l.Source == "stderr" && l.Text == "starting npx" }) {
		t.Errorf("logs = %+v, want the process's stderr", d.Logs)
	}

	pool.StopWorkspace("ws1")
	if st := pool.Status("s1"); !slices.Equal(st.Workspaces, []string{"ws2"}) {
		t.Errorf("status after stopping ws1 = %+v", st)
	}
}

func TestPoolReconnectsAfterTheSessionExpires(t *testing.T) {
	s := &mcptest.Server{Era: mcptest.Legacy, Tools: []mcptest.Tool{echoTool("echo")}}
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "docs", Kind: mcp.KindHTTP, URL: serve(t, s), Enabled: true}}}
	pool := newPool(t, store, mcp.Options{})
	tools := pool.Tools(context.Background(), "")
	s.ExpireSessions()
	res, err := tools[0].Call(context.Background(), tool.CallContext{}, json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("Call after expiry = %+v, %v; want it sent again on a new session", res, err)
	}
	var initializes int
	for _, m := range s.Methods() {
		if m == "initialize" {
			initializes++
		}
	}
	if initializes != 2 {
		t.Errorf("initialized %d times, want 2", initializes)
	}
}

func TestPoolAsksTheUserThroughTheBroker(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{confirmTool(mcptest.Modern)}}
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "docs", Kind: mcp.KindHTTP, URL: serve(t, s), Enabled: true}}}
	rec := newRecorder()
	broker := mcp.NewElicitations(rec)
	pool := newPool(t, store, mcp.Options{Emitter: rec, Elicitations: broker})
	tools := pool.Tools(context.Background(), "")

	done := make(chan tool.Result, 1)
	go func() {
		res, _ := tools[0].Call(context.Background(), tool.CallContext{SessionID: "sess", RunID: "run", CallID: "call"}, nil)
		done <- res
	}()
	e := rec.waitFor(t, event.TypeMCPElicitation)
	var asked event.MCPElicitation
	if err := json.Unmarshal(e.Payload, &asked); err != nil {
		t.Fatal(err)
	}
	if e.Topic != event.SessionTopic("sess") || asked.CallID != "call" || asked.Server != "docs" || asked.Mode != "form" {
		t.Errorf("elicitation = %+v on %s", asked, e.Topic)
	}
	if pending := broker.Pending(); len(pending) != 1 || pending[0].ID != asked.ElicitationID {
		t.Errorf("pending = %+v", pending)
	}
	if err := broker.Answer(asked.ElicitationID, mcp.ElicitResult{Action: mcp.ElicitAccept, Content: json.RawMessage(`{"name":7}`)}); !errors.Is(err, mcp.ErrBadElicitationAnswer) {
		t.Errorf("a wrongly typed answer = %v, want ErrBadElicitationAnswer", err)
	}
	if err := broker.Answer(asked.ElicitationID, mcp.ElicitResult{Action: mcp.ElicitAccept, Content: json.RawMessage(`{"name":"Ada"}`)}); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	res := <-done
	if !strings.Contains(res.Content, `"Ada"`) {
		t.Errorf("result = %q", res.Content)
	}
	if err := broker.Answer(asked.ElicitationID, mcp.ElicitResult{Action: mcp.ElicitDecline}); !errors.Is(err, mcp.ErrNoElicitation) {
		t.Errorf("a second answer = %v, want ErrNoElicitation", err)
	}
}

// protected is an MCP server behind the fake authorization server.
type protected struct {
	auth     *mcptest.Authorization
	server   *mcptest.Server
	url      string
	resource string
}

func newProtected(t *testing.T, scopes ...string) *protected {
	t.Helper()
	p := &protected{auth: mcptest.NewAuthorization(t)}
	p.server = &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	mux := http.NewServeMux()
	mux.Handle("/mcp", p.server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p.url = srv.URL + "/mcp"
	p.resource = p.url
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp", p.auth.ProtectedResource(p.resource, scopes...))
	p.server.Authorize = func(header string) bool { return p.auth.Valid(header, p.resource) }
	p.server.Challenge = fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, srv.URL)
	return p
}

const redirect = "http://localhost:5173/mcp/callback"

// authorize runs the whole browser round trip and returns the server's id.
func authorize(t *testing.T, pool *mcp.Pool, a *mcptest.Authorization, id string) string {
	t.Helper()
	ctx := context.Background()
	target, err := pool.BeginAuthorization(ctx, id, redirect)
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}
	back, err := a.Approve(ctx, target)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	q := back.Query()
	_, hasIss := q["iss"]
	got, err := pool.CompleteAuthorization(ctx, mcp.Callback{State: q.Get("state"), Code: q.Get("code"), Iss: q.Get("iss"), IssPresent: hasIss})
	if err != nil {
		t.Fatalf("CompleteAuthorization: %v", err)
	}
	return got
}

func TestPoolAuthorizesAServer(t *testing.T) {
	p := newProtected(t, "tools:read")
	p.auth.IssParameter = true
	p.auth.Scopes = []string{"tools:read", "offline_access"}
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "secure", Kind: mcp.KindHTTP, URL: p.url, Enabled: true}}}
	pool := newPool(t, store, mcp.Options{})
	ctx := context.Background()

	if got := pool.Tools(ctx, ""); len(got) != 0 {
		t.Fatalf("tools before authorizing = %v", toolNames(got))
	}
	if st := pool.Status("s1"); st.State != mcp.StateUnauthorized {
		t.Fatalf("status = %+v, want unauthorized", st)
	}
	d, _ := pool.Details(ctx, "s1")
	if !d.Auth.Challenged || !strings.Contains(d.Auth.Challenge.ResourceMetadata, "oauth-protected-resource") {
		t.Errorf("auth = %+v, want the challenge", d.Auth)
	}

	target, err := pool.BeginAuthorization(ctx, "s1", redirect)
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}
	for _, want := range []string{"code_challenge_method=S256", "scope=tools%3Aread+offline_access", "resource="} {
		if !strings.Contains(target, want) {
			t.Errorf("authorization URL %s lacks %s", target, want)
		}
	}
	back, err := p.auth.Approve(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	q := back.Query()
	if _, err := pool.CompleteAuthorization(ctx, mcp.Callback{State: q.Get("state"), Code: q.Get("code"), Iss: "https://evil.example", IssPresent: true}); err == nil {
		t.Fatal("CompleteAuthorization accepted an answer from another issuer")
	}
	if len(p.auth.TokenRequests()) != 0 {
		t.Error("the code was redeemed despite the wrong issuer")
	}
	if _, err := pool.CompleteAuthorization(ctx, mcp.Callback{State: q.Get("state"), Code: q.Get("code"), Iss: q.Get("iss"), IssPresent: true}); !errors.Is(err, mcp.ErrNoAuthorization) {
		t.Errorf("a second answer for the same state = %v, want ErrNoAuthorization", err)
	}

	if id := authorize(t, pool, p.auth, "s1"); id != "s1" {
		t.Errorf("CompleteAuthorization returned %s", id)
	}
	if regs := p.auth.Registrations(); len(regs) != 1 {
		t.Errorf("registered %d clients, want the first one reused", len(regs))
	}
	if st := pool.Status("s1"); st.State != mcp.StateConnected {
		t.Fatalf("status after authorizing = %+v", st)
	}
	d, _ = pool.Details(ctx, "s1")
	if !d.Auth.Authorized || !d.Auth.RefreshToken || d.Auth.Challenged || d.Auth.Registration != oauth.RegisteredDynamically || d.Auth.Issuer != p.auth.Issuer {
		t.Errorf("auth = %+v", d.Auth)
	}
	creds, _, _ := store.MCPCredentials(ctx, "s1")
	if creds.Resource != p.resource || creds.Scope != "tools:read offline_access" {
		t.Errorf("credentials = %+v", creds)
	}

	// An expired token is refreshed and the call goes through.
	tools := pool.Tools(ctx, "")
	p.auth.Expire()
	res, err := find(tools, "mcp__secure__echo").Call(ctx, tool.CallContext{}, json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("Call after the token expired = %+v, %v", res, err)
	}
	refreshed, _, _ := store.MCPCredentials(ctx, "s1")
	if refreshed.AccessToken == creds.AccessToken || refreshed.RefreshToken == creds.RefreshToken {
		t.Error("the tokens were not refreshed")
	}

	// Signing out revokes both tokens and keeps the client.
	if err := pool.SignOut(ctx, "s1"); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	if got := p.auth.Revoked(); !slices.Equal(got, []string{refreshed.RefreshToken, refreshed.AccessToken}) {
		t.Errorf("revoked %v", got)
	}
	after, _, _ := store.MCPCredentials(ctx, "s1")
	if after.AccessToken != "" || after.RefreshToken != "" || after.Client.ID != creds.Client.ID {
		t.Errorf("credentials after signing out = %+v", after)
	}
}

func TestPoolUsesTheClientRegisteredByHand(t *testing.T) {
	p := newProtected(t)
	p.auth.NoRegistration = true
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "secure", Kind: mcp.KindHTTP, URL: p.url, Enabled: true}}}
	pool := newPool(t, store, mcp.Options{})
	ctx := context.Background()
	if _, err := pool.BeginAuthorization(ctx, "s1", redirect); err == nil || !strings.Contains(err.Error(), "register a client there by hand") {
		t.Fatalf("BeginAuthorization with no way to register = %v", err)
	}
	store.update("s1", func(c *mcp.ServerConfig) { c.OAuthClientID = "by-hand" })
	target, err := pool.BeginAuthorization(ctx, "s1", redirect)
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}
	if !strings.Contains(target, "client_id=by-hand") {
		t.Errorf("authorization URL %s does not use the client entered", target)
	}
}

func TestPoolUsesTheMetadataDocument(t *testing.T) {
	p := newProtected(t)
	p.auth.MetadataDocuments = true
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "secure", Kind: mcp.KindHTTP, URL: p.url, Enabled: true}}}
	pool := newPool(t, store, mcp.Options{PublicURL: "https://eika.example"})
	doc, ok := pool.ClientMetadataDocument()
	if !ok || doc["client_id"] != "https://eika.example/oauth/client-metadata.json" {
		t.Fatalf("document = %v, %v", doc, ok)
	}
	target, err := pool.BeginAuthorization(context.Background(), "s1", "https://eika.example/mcp/callback")
	if err != nil {
		t.Fatalf("BeginAuthorization: %v", err)
	}
	if !strings.Contains(target, "client_id=https%3A%2F%2Feika.example%2Foauth%2Fclient-metadata.json") || len(p.auth.Registrations()) != 0 {
		t.Errorf("authorization URL %s, %d registrations; want the document's id", target, len(p.auth.Registrations()))
	}
	if _, ok := mcp.NewPool(mcp.Options{PublicURL: "http://eika.example"}).ClientMetadataDocument(); ok {
		t.Error("a plain http deployment serves a metadata document")
	}
}

func TestPoolRefusesAnAuthorizationServerWithoutPKCE(t *testing.T) {
	p := newProtected(t)
	p.auth.NoPKCE = true
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "secure", Kind: mcp.KindHTTP, URL: p.url, Enabled: true}}}
	pool := newPool(t, store, mcp.Options{})
	if _, err := pool.BeginAuthorization(context.Background(), "s1", redirect); err == nil || !strings.Contains(err.Error(), "PKCE") {
		t.Errorf("BeginAuthorization = %v, want a PKCE refusal", err)
	}
}

func TestConfiguredAuthorizationHeaderWins(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}, Authorize: func(h string) bool { return h == "Bearer static" }}
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "docs", Kind: mcp.KindHTTP, URL: serve(t, s), Enabled: true, Headers: map[string]string{"Authorization": "Bearer static"}}}}
	pool := newPool(t, store, mcp.Options{})
	if got := pool.Tools(context.Background(), ""); len(got) != 1 {
		t.Errorf("tools = %v, want the server reached with the configured header", toolNames(got))
	}
}

func TestOfferedComesFromTheLastListing(t *testing.T) {
	s := &mcptest.Server{
		Tools:     []mcptest.Tool{echoTool("echo"), echoTool("off")},
		Resources: []json.RawMessage{json.RawMessage(`{"uri":"docs://a","name":"a"}`)},
	}
	store := &memStore{servers: []mcp.ServerConfig{
		{ID: "s1", Name: "docs", Kind: mcp.KindHTTP, URL: serve(t, s), Enabled: true, DisabledTools: []string{"off"}},
		{ID: "s2", Name: "local", Kind: mcp.KindStdio, Command: "npx", Enabled: true},
	}}
	pool := newPool(t, store, mcp.Options{})
	if got := pool.Offered(); len(got) != 0 {
		t.Errorf("offered before any listing = %v", toolNames(got))
	}
	pool.Tools(context.Background(), "")
	// A server that goes down keeps what it last listed on offer.
	store.update("s1", func(c *mcp.ServerConfig) { c.URL = "http://127.0.0.1:1/mcp" })
	offered := pool.Offered()
	if got := toolNames(offered); !slices.Equal(got, []string{"mcp__docs__echo", "mcp_list_resources", "mcp_read_resource"}) {
		t.Fatalf("offered = %v", got)
	}
	if mcp.ServerOf(offered[0]) != "docs" || mcp.ServerOf(offered[1]) != "" {
		t.Errorf("servers = %q, %q", mcp.ServerOf(offered[0]), mcp.ServerOf(offered[1]))
	}
	pool.Changed(context.Background(), "s1")
	store.update("s1", func(c *mcp.ServerConfig) { c.Enabled = false })
	pool.Changed(context.Background(), "s1")
	if got := pool.Offered(); len(got) != 0 {
		t.Errorf("offered after turning the server off = %v", toolNames(got))
	}
}

func TestRemovingAServerReportsIt(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	store := &memStore{servers: []mcp.ServerConfig{{ID: "s1", Name: "docs", Kind: mcp.KindHTTP, URL: serve(t, s), Enabled: true}}}
	rec := newRecorder()
	pool := newPool(t, store, mcp.Options{Emitter: rec})
	pool.Tools(context.Background(), "")
	pool.Removed("s1")
	for {
		e := rec.waitFor(t, event.TypeMCPServer)
		var p event.MCPServer
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.State == mcp.StateRemoved {
			if p.ServerID != "s1" || p.Name != "docs" {
				t.Errorf("removed event = %+v", p)
			}
			break
		}
	}
	if got := pool.Offered(); len(got) != 0 {
		t.Errorf("offered after removal = %v", toolNames(got))
	}
}
