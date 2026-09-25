package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/mcp/oauth"
)

// The states a server is reported in.
const (
	StateDisabled   = "disabled"
	StateIdle       = "idle"
	StateConnecting = "connecting"
	StateConnected  = "connected"
	// StateUnauthorized is a server that refused the harness for want of an
	// access token, and waits for the user to authorize it.
	StateUnauthorized = "unauthorized"
	StateError        = "error"
	// StateRemoved is the last state of a server the user deleted.
	StateRemoved = "removed"
)

// The ways a user configures a server to be reached.
const (
	// KindHTTP is a remote server the harness connects to.
	KindHTTP = "http"
	// KindStdio is a command started in the workspace of each session
	// that uses it.
	KindStdio = "stdio"
)

// ErrDisabled reports a server the user turned off.
var ErrDisabled = errors.New("the server is turned off")

// ErrNeedsWorkspace reports a stdio server asked for outside a workspace.
var ErrNeedsWorkspace = errors.New("a stdio server runs in a workspace, and none was given")

// ServerConfig is one server the user configured, with its secrets open.
type ServerConfig struct {
	ID   string
	Name string
	// Kind is KindHTTP or KindStdio.
	Kind string
	URL  string
	// Headers are sent with every request to an HTTP server.
	Headers map[string]string
	Command string
	Args    []string
	// Env holds KEY=VALUE entries for a stdio server's process.
	Env           []string
	Enabled       bool
	DisabledTools []string
	// OAuthClientID and OAuthClientSecret are a client the user registered
	// with the server's authorization server by hand.
	OAuthClientID     string
	OAuthClientSecret string
}

// Credentials are what authorizing a server left behind: its authorization
// server, the client registered there, and the tokens it issued.
type Credentials struct {
	Issuer              string
	Resource            string
	ResourceMetadataURL string
	Server              oauth.ServerMetadata
	Client              oauth.Client
	RedirectURI         string
	AccessToken         string
	RefreshToken        string
	Scope               string
	ExpiresAt           time.Time
	UpdatedAt           time.Time
}

// Store is where the pool reads the configured servers and keeps their
// credentials. The server implements it over the database, sealing and
// opening the secrets.
type Store interface {
	MCPServers(ctx context.Context) ([]ServerConfig, error)
	MCPServer(ctx context.Context, id string) (ServerConfig, error)
	// MCPCredentials reports false when the server has none.
	MCPCredentials(ctx context.Context, serverID string) (Credentials, bool, error)
	SaveMCPCredentials(ctx context.Context, serverID string, c Credentials) error
}

// Command is a stdio server's process.
type Command struct {
	Name string
	Args []string
	Env  []string
}

// Launcher starts a stdio server's process where the harness runs agent
// processes: in a workspace.
type Launcher interface {
	// Launch starts cmd in a running workspace. Writes reach the process's
	// stdin, reads come from its stdout, and Close ends it. Every line it
	// writes to stderr is passed to stderr.
	Launch(ctx context.Context, workspaceID string, cmd Command, stderr func(string)) (io.ReadWriteCloser, error)
}

// Options configure a Pool.
type Options struct {
	Store        Store
	Emitter      event.Emitter
	Elicitations *Elicitations
	// HTTPClient reaches remote servers and authorization servers.
	HTTPClient *http.Client
	// Launcher starts stdio servers; without one they cannot run.
	Launcher Launcher
	// Client is how Eika introduces itself to a server.
	Client Implementation
	// PublicURL is the https address the deployment is reached at, where a
	// Client ID Metadata Document can be fetched; empty when there is none.
	PublicURL string
	Logger    *slog.Logger
}

// Pool keeps the connections to the servers the user configured: one to
// each remote server, and one to each stdio server in each workspace that
// runs it. It connects on demand, reports every change of state, and hands a
// run the tools of the servers it can reach.
type Pool struct {
	opts Options
	log  *slog.Logger

	// ctx lives as long as the pool: a connection outlives the request that
	// asked for it.
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu      sync.Mutex
	stopped bool
	servers map[string]*server
	// flows are the authorizations waiting for the browser to come back,
	// by state.
	flows map[string]*authFlow
}

// server is what the pool knows of one configured server.
type server struct {
	id string

	mu      sync.Mutex
	name    string
	kind    string
	enabled bool
	// disabled are the tools the user turned off.
	disabled []string
	// remote is the connection to an HTTP server; local holds a stdio
	// server's, by workspace.
	remote *conn
	local  map[string]*conn
	// catalog is what the latest connection listed, kept after it closes
	// so the UI can still show it.
	catalog Catalog
	// challenge is what the server last said when it refused a token.
	challenge *oauth.Challenge
	// token caches the access token between requests.
	token *cachedToken
	logs  []LogLine
	// refreshMu makes refreshes of one server's token take turns.
	refreshMu sync.Mutex
}

// conn is one connection and what it serves.
type conn struct {
	workspace string
	// ready is closed when connecting finished, one way or the other;
	// cancelOpen ends a connection attempt that is no longer wanted.
	ready      chan struct{}
	cancelOpen context.CancelFunc
	client     *Client
	err        error
	attempted  time.Time
	lists      Catalog
	// stopListening ends a modern connection's subscription.
	stopListening context.CancelFunc
}

// Catalog is what a server serves, as one connection listed it.
type Catalog struct {
	Info      Info
	Tools     []Tool
	Excluded  []ExcludedTool
	Resources []Resource
	Templates []ResourceTemplate
	Prompts   []Prompt
	// Errors are the lists that could not be read, by method.
	Errors    map[string]string
	FetchedAt time.Time
}

// ExcludedTool is a tool the server lists that the client may not offer.
type ExcludedTool struct {
	Name   string
	Reason string
}

// LogLine is one line of a server's log as the UI shows it.
type LogLine struct {
	Time time.Time
	// Source is stderr, server (a log notification), or eika (what the
	// client did).
	Source string
	Level  string
	Text   string
}

// Status is a server's state as the UI reports it.
type Status struct {
	State string
	Error string
	// Workspaces are where a stdio server is running.
	Workspaces []string
}

// NewPool returns a pool over the configured servers. Start connects the
// remote ones.
func NewPool(opts Options) *Pool {
	if opts.Emitter == nil {
		opts.Emitter = event.Discard
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	if opts.Elicitations == nil {
		opts.Elicitations = NewElicitations(opts.Emitter)
	}
	ctx, stop := context.WithCancel(context.Background())
	return &Pool{opts: opts, log: opts.Logger, ctx: ctx, stop: stop, servers: map[string]*server{}, flows: map[string]*authFlow{}}
}

// Start connects every enabled remote server in the background, so their
// tools are there when the first run asks.
func (p *Pool) Start(ctx context.Context) error {
	cfgs, err := p.opts.Store.MCPServers(ctx)
	if err != nil {
		return fmt.Errorf("list mcp servers: %w", err)
	}
	for _, cfg := range cfgs {
		s := p.serverFor(cfg)
		if cfg.Enabled && cfg.Kind == KindHTTP {
			p.connectInBackground(s)
		}
	}
	return nil
}

// Close ends every connection and waits for the pool's goroutines.
func (p *Pool) Close() {
	p.mu.Lock()
	p.stopped = true
	servers := make([]*server, 0, len(p.servers))
	for _, s := range p.servers {
		servers = append(servers, s)
	}
	p.mu.Unlock()
	p.stop()
	for _, s := range servers {
		for _, c := range s.takeConns("") {
			c.close()
		}
	}
	p.wg.Wait()
}

// serverFor returns the pool's record of a server, making it on first sight,
// and brings its name and switches up to date.
func (p *Pool) serverFor(cfg ServerConfig) *server {
	p.mu.Lock()
	s, ok := p.servers[cfg.ID]
	if !ok {
		s = &server{id: cfg.ID, local: map[string]*conn{}}
		p.servers[cfg.ID] = s
	}
	p.mu.Unlock()
	s.mu.Lock()
	s.name, s.kind, s.enabled, s.disabled = cfg.Name, cfg.Kind, cfg.Enabled, slices.Clone(cfg.DisabledTools)
	s.mu.Unlock()
	return s
}

// lookup returns the pool's record of a server, loading it when the pool
// has not seen it yet.
func (p *Pool) lookup(ctx context.Context, id string) (*server, ServerConfig, error) {
	cfg, err := p.opts.Store.MCPServer(ctx, id)
	if err != nil {
		return nil, ServerConfig{}, err
	}
	return p.serverFor(cfg), cfg, nil
}

// Changed takes a server's new configuration: its connections end, and a
// remote server that is on connects again.
func (p *Pool) Changed(ctx context.Context, id string) {
	s, cfg, err := p.lookup(ctx, id)
	if err != nil {
		p.log.Error("reload mcp server", "server_id", id, "error", err)
		return
	}
	for _, c := range s.takeConns("") {
		c.close()
	}
	s.mu.Lock()
	s.token, s.challenge = nil, nil
	s.mu.Unlock()
	s.addLog("eika", "info", "configuration changed")
	p.emit(s)
	if cfg.Enabled && cfg.Kind == KindHTTP {
		p.connectInBackground(s)
	}
}

// Removed forgets a deleted server, ends its connections, and reports it
// gone.
func (p *Pool) Removed(id string) {
	p.mu.Lock()
	s := p.servers[id]
	delete(p.servers, id)
	for state, f := range p.flows {
		if f.serverID == id {
			delete(p.flows, state)
		}
	}
	p.mu.Unlock()
	name := ""
	if s != nil {
		for _, c := range s.takeConns("") {
			c.close()
		}
		s.mu.Lock()
		name = s.name
		s.mu.Unlock()
	}
	p.emitState(id, name, Status{State: StateRemoved})
}

// StopWorkspace ends the stdio servers running in a workspace that is
// stopping, whose processes are about to end with it.
func (p *Pool) StopWorkspace(workspaceID string) {
	p.mu.Lock()
	servers := make([]*server, 0, len(p.servers))
	for _, s := range p.servers {
		servers = append(servers, s)
	}
	p.mu.Unlock()
	for _, s := range servers {
		conns := s.takeConns(workspaceID)
		for _, c := range conns {
			c.close()
		}
		if len(conns) > 0 {
			p.emit(s)
		}
	}
}

// Reconnect drops a server's connection and connects it again, in the given
// workspace for a stdio server. It returns once the attempt finished.
func (p *Pool) Reconnect(ctx context.Context, id, workspaceID string) error {
	s, cfg, err := p.lookup(ctx, id)
	if err != nil {
		return err
	}
	key := ""
	if cfg.Kind == KindStdio {
		key = workspaceID
	}
	s.mu.Lock()
	var old *conn
	if key == "" {
		old, s.remote = s.remote, nil
	} else {
		old = s.local[key]
		delete(s.local, key)
	}
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
	_, err = p.connection(ctx, id, workspaceID, true)
	return err
}

// connectInBackground starts connecting a server without waiting for it.
func (p *Pool) connectInBackground(s *server) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ctx, cancel := context.WithTimeout(p.ctx, 2*requestTimeout)
		defer cancel()
		if _, err := p.connection(ctx, s.id, "", false); err != nil && !errors.Is(err, context.Canceled) {
			p.log.Info("mcp server not connected", "server_id", s.id, "error", err)
		}
	}()
}

// connection returns a live connection to a server, connecting when there
// is none. A connection that failed recently is not tried again unless
// retry says so, so a server that is down costs each run nothing.
func (p *Pool) connection(ctx context.Context, id, workspaceID string, retry bool) (*conn, error) {
	s, cfg, err := p.lookup(ctx, id)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, ErrDisabled
	}
	key := ""
	if cfg.Kind == KindStdio {
		if workspaceID == "" {
			return nil, ErrNeedsWorkspace
		}
		key = workspaceID
	}
	s.mu.Lock()
	c := s.connFor(key)
	switch {
	case c == nil:
	case !c.finished():
	case c.alive():
		s.mu.Unlock()
		return c, nil
	case c.err != nil && !retry && time.Since(c.attempted) < retryAfter:
		s.mu.Unlock()
		return nil, c.err
	default:
		c = nil
	}
	fresh := c == nil
	var openCtx context.Context
	if fresh {
		c = &conn{workspace: key, ready: make(chan struct{}), attempted: time.Now()}
		openCtx, c.cancelOpen = context.WithTimeout(p.ctx, 2*requestTimeout)
		s.setConn(key, c)
	}
	s.mu.Unlock()
	if fresh {
		p.mu.Lock()
		stopped := p.stopped
		if !stopped {
			p.wg.Add(1)
		}
		p.mu.Unlock()
		if stopped {
			c.cancelOpen()
			return nil, errors.New("the harness is shutting down")
		}
		p.emit(s)
		go func() {
			defer p.wg.Done()
			defer c.cancelOpen()
			p.open(openCtx, s, c, cfg)
		}()
	}
	select {
	case <-c.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	err = c.err
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// open connects one conn and lists what the server serves.
func (p *Pool) open(ctx context.Context, s *server, c *conn, cfg ServerConfig) {
	handlers := Handlers{
		Notify: func(method string, params json.RawMessage) { p.notified(s, c, method, params) },
		Closed: func(err error) { p.dropped(s, c, err) },
	}
	client, err := p.dial(ctx, s, c, cfg, handlers)
	if err != nil {
		p.failed(s, c, err)
		return
	}
	lists := p.catalog(ctx, client)
	s.mu.Lock()
	current := s.connFor(c.workspace) == c
	if current {
		c.client, c.lists = client, lists
		s.catalog = lists
	}
	s.mu.Unlock()
	if !current {
		// The configuration changed while this connected; it is stale.
		client.Close()
		s.mu.Lock()
		c.err = errors.New("the server's configuration changed while connecting")
		s.mu.Unlock()
		close(c.ready)
		return
	}
	info := client.Info()
	s.addLog("eika", "info", fmt.Sprintf("connected: %s %s over %s%s", info.Era, info.Version, info.Transport, where(c.workspace)))
	if client.listens() {
		listenCtx, stopListening := context.WithCancel(p.ctx)
		c.stopListening = stopListening
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.listen(listenCtx, s, c)
		}()
	}
	close(c.ready)
	p.emit(s)
}

// dial opens the connection itself: over HTTP with the configured headers
// and the access token, refreshing an expired token once, or over a process
// the launcher starts in the workspace.
func (p *Pool) dial(ctx context.Context, s *server, c *conn, cfg ServerConfig, handlers Handlers) (*Client, error) {
	switch cfg.Kind {
	case KindHTTP:
		target := HTTPTarget{URL: cfg.URL, Client: p.opts.HTTPClient, Headers: p.headers(s, cfg)}
		client, err := ConnectHTTP(ctx, target, p.opts.Client, handlers)
		var auth *AuthError
		if errors.As(err, &auth) && p.refreshAfter(ctx, s, auth) {
			client, err = ConnectHTTP(ctx, target, p.opts.Client, handlers)
		}
		return client, err
	case KindStdio:
		if p.opts.Launcher == nil {
			return nil, errors.New("this harness cannot start stdio servers")
		}
		stderr := func(line string) { s.addLog("stderr", "info", line) }
		rw, err := p.opts.Launcher.Launch(ctx, c.workspace, Command{Name: cfg.Command, Args: cfg.Args, Env: cfg.Env}, stderr)
		if err != nil {
			return nil, fmt.Errorf("start %s: %w", cfg.Command, err)
		}
		client, err := ConnectStdio(ctx, rw, p.opts.Client, handlers)
		if err != nil {
			_ = rw.Close()
			return nil, err
		}
		return client, nil
	}
	return nil, fmt.Errorf("unknown server kind %q", cfg.Kind)
}

// failed records a connection that did not come up.
func (p *Pool) failed(s *server, c *conn, err error) {
	var auth *AuthError
	if errors.As(err, &auth) {
		s.mu.Lock()
		challenge := auth.Challenge
		s.challenge = &challenge
		s.mu.Unlock()
		s.addLog("eika", "warning", "the server asks for authorization")
	} else {
		s.addLog("eika", "error", fmt.Sprintf("connection failed%s: %v", where(c.workspace), err))
	}
	s.mu.Lock()
	c.err = err
	s.mu.Unlock()
	close(c.ready)
	p.emit(s)
}

// catalog lists everything a server says it serves. A list that fails is
// recorded rather than failing the connection: the tools still work when
// the prompts do not.
func (p *Pool) catalog(ctx context.Context, client *Client) Catalog {
	info := client.Info()
	cat := Catalog{Info: info, Errors: map[string]string{}, FetchedAt: time.Now().UTC()}
	caps := info.Capabilities
	if caps.Tools != nil {
		p.listTools(ctx, client, &cat)
	}
	if caps.Resources != nil {
		if list, err := client.ListResources(ctx); err != nil {
			cat.Errors["resources/list"] = err.Error()
		} else {
			cat.Resources = list
		}
		if list, err := client.ListResourceTemplates(ctx); err != nil {
			var rpc *RPCError
			// Templates are optional within the capability.
			if !errors.As(err, &rpc) || rpc.Code != codeMethodNotFound {
				cat.Errors["resources/templates/list"] = err.Error()
			}
		} else {
			cat.Templates = list
		}
	}
	if caps.Prompts != nil {
		if list, err := client.ListPrompts(ctx); err != nil {
			cat.Errors["prompts/list"] = err.Error()
		} else {
			cat.Prompts = list
		}
	}
	return cat
}

// listTools lists the tools, setting aside those a Streamable HTTP client
// must not offer: a modern server's tool whose x-mcp-header annotations
// break the rules.
func (p *Pool) listTools(ctx context.Context, client *Client, cat *Catalog) {
	tools, err := client.ListTools(ctx)
	if err != nil {
		cat.Errors["tools/list"] = err.Error()
		return
	}
	info := client.Info()
	checkHeaders := info.Transport == TransportStreamableHTTP && info.Era == EraModern
	cat.Tools, cat.Excluded = cat.Tools[:0], nil
	for _, t := range tools {
		if checkHeaders {
			if _, err := headerParams(t.InputSchema); err != nil {
				cat.Excluded = append(cat.Excluded, ExcludedTool{Name: t.Name, Reason: err.Error()})
				continue
			}
		}
		cat.Tools = append(cat.Tools, t)
	}
}

// notified handles what a server sends unasked: a list that changed is read
// again, and a log message is kept.
func (p *Pool) notified(s *server, c *conn, method string, params json.RawMessage) {
	switch method {
	case notifyToolsChanged, notifyResourcesChanged, notifyPromptsChanged:
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.relist(s, c)
		}()
	case notifyMessage:
		var lp logParams
		if err := json.Unmarshal(params, &lp); err == nil {
			text := strings.Trim(string(lp.Data), `"`)
			if lp.Logger != "" {
				text = lp.Logger + ": " + text
			}
			s.addLog("server", lp.Level, text)
		}
	}
}

// relist reads a connection's lists again and reports the change.
func (p *Pool) relist(s *server, c *conn) {
	s.mu.Lock()
	client := c.client
	s.mu.Unlock()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(p.ctx, requestTimeout)
	defer cancel()
	lists := p.catalog(ctx, client)
	s.mu.Lock()
	c.lists = lists
	if s.connFor(c.workspace) == c {
		s.catalog = lists
	}
	s.mu.Unlock()
	p.emit(s)
}

// listen keeps a modern connection's subscription open, reading the lists
// again whenever it had to open it anew, since changes in between were
// missed.
func (p *Pool) listen(ctx context.Context, s *server, c *conn) {
	backoff := time.Second
	for {
		err := c.client.Listen(ctx)
		if ctx.Err() != nil {
			return
		}
		var (
			rpc    *RPCError
			status *HTTPError
		)
		if errors.As(err, &rpc) || errors.As(err, &status) {
			// The server refused the subscription; asking again will not
			// change its mind.
			s.addLog("eika", "warning", "the server refused a subscription to its list changes: "+err.Error())
			return
		}
		if closed, _ := c.client.Done(); closed {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, time.Minute)
		p.relist(s, c)
	}
}

// dropped records that a connection over a shared channel ended by itself.
// It runs on the transport's goroutine, so the closing is left to another.
func (p *Pool) dropped(s *server, c *conn, err error) {
	s.mu.Lock()
	current := s.connFor(c.workspace) == c
	if current {
		c.err = err
	}
	s.mu.Unlock()
	if !current {
		return
	}
	s.addLog("eika", "error", fmt.Sprintf("connection lost%s: %v", where(c.workspace), err))
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		c.close()
	}()
	p.emit(s)
}

// listsOf reads what a connection listed.
func (p *Pool) listsOf(id string, c *conn) Catalog {
	p.mu.Lock()
	s := p.servers[id]
	p.mu.Unlock()
	if s == nil {
		return Catalog{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return c.lists
}

// Status reports a server's state.
func (p *Pool) Status(id string) Status {
	p.mu.Lock()
	s := p.servers[id]
	p.mu.Unlock()
	if s == nil {
		return Status{State: StateIdle}
	}
	return s.status()
}

// Details is everything the pool knows of a server.
type Details struct {
	Status
	Catalog Catalog
	Logs    []LogLine
	Auth    AuthStatus
}

// Details reports a server's state, what it serves, its log, and its
// authorization.
func (p *Pool) Details(ctx context.Context, id string) (Details, error) {
	s, _, err := p.lookup(ctx, id)
	if err != nil {
		return Details{}, err
	}
	auth, err := p.authStatus(ctx, s)
	if err != nil {
		return Details{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return Details{
		Status:  s.statusLocked(),
		Catalog: s.catalog,
		Logs:    slices.Clone(s.logs),
		Auth:    auth,
	}, nil
}

// emit reports a server's state on the global topic.
func (p *Pool) emit(s *server) {
	st := s.status()
	s.mu.Lock()
	name := s.name
	s.mu.Unlock()
	p.emitState(s.id, name, st)
}

// emitState sends one mcp.server event.
func (p *Pool) emitState(id, name string, st Status) {
	e, err := event.New(event.TypeMCPServer, event.TopicGlobal, event.MCPServer{
		ServerID: id,
		Name:     name,
		State:    st.State,
		Error:    st.Error,
	})
	if err != nil {
		p.log.Error("encode mcp server event", "server_id", id, "error", err)
		return
	}
	p.opts.Emitter.Emit(p.ctx, e)
}

// Elicitations is the broker the pool's calls ask the user through.
func (p *Pool) Elicitations() *Elicitations { return p.opts.Elicitations }

// status reports the server's state.
func (s *server) status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

// statusLocked derives the state from the connections: a remote server's
// one, or the most telling of a stdio server's.
func (s *server) statusLocked() Status {
	if !s.enabled {
		return Status{State: StateDisabled}
	}
	conns := []*conn{}
	if s.remote != nil {
		conns = append(conns, s.remote)
	}
	for _, c := range s.local {
		conns = append(conns, c)
	}
	out := Status{State: StateIdle}
	var failure error
	for _, c := range conns {
		switch {
		case !c.finished():
			if out.State != StateConnected {
				out.State = StateConnecting
			}
		case c.alive():
			out.State = StateConnected
			if c.workspace != "" {
				out.Workspaces = append(out.Workspaces, c.workspace)
			}
		case c.err != nil:
			failure = c.err
		}
	}
	slices.Sort(out.Workspaces)
	if out.State == StateIdle && failure != nil {
		var auth *AuthError
		if errors.As(failure, &auth) && auth.Status == http.StatusUnauthorized {
			return Status{State: StateUnauthorized, Error: failure.Error()}
		}
		return Status{State: StateError, Error: failure.Error()}
	}
	return out
}

// connFor returns the connection for a workspace, or the remote one.
func (s *server) connFor(workspace string) *conn {
	if workspace == "" {
		return s.remote
	}
	return s.local[workspace]
}

// setConn records a new connection.
func (s *server) setConn(workspace string, c *conn) {
	if workspace == "" {
		s.remote = c
		return
	}
	s.local[workspace] = c
}

// takeConns removes and returns the connections in a workspace, or every
// connection when workspace is empty.
func (s *server) takeConns(workspace string) []*conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*conn
	if workspace == "" {
		if s.remote != nil {
			out = append(out, s.remote)
			s.remote = nil
		}
		for key, c := range s.local {
			out = append(out, c)
			delete(s.local, key)
		}
		return out
	}
	if c, ok := s.local[workspace]; ok {
		out = append(out, c)
		delete(s.local, workspace)
	}
	return out
}

// addLog keeps one line of the server's log.
func (s *server) addLog(source, level, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if len(line) > maxLogLine {
			line = line[:maxLogLine] + "…"
		}
		s.mu.Lock()
		s.logs = append(s.logs, LogLine{Time: time.Now().UTC(), Source: source, Level: level, Text: line})
		if over := len(s.logs) - maxLogLines; over > 0 {
			s.logs = slices.Delete(s.logs, 0, over)
		}
		s.mu.Unlock()
	}
}

// finished reports whether connecting is over.
func (c *conn) finished() bool {
	select {
	case <-c.ready:
		return true
	default:
		return false
	}
}

// alive reports whether a finished connection is up.
func (c *conn) alive() bool {
	if c.err != nil || c.client == nil {
		return false
	}
	closed, _ := c.client.Done()
	return !closed
}

// close ends a connection. One still connecting is told to stop, and close
// waits for it to.
func (c *conn) close() {
	c.cancelOpen()
	<-c.ready
	if c.stopListening != nil {
		c.stopListening()
	}
	if c.client != nil {
		c.client.Close()
	}
}

// where names the workspace a connection is in, for a log line.
func where(workspace string) string {
	if workspace == "" {
		return ""
	}
	return " in workspace " + workspace
}
