package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
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

// Elicitations is the broker the pool's calls ask the user through.
func (p *Pool) Elicitations() *Elicitations { return p.opts.Elicitations }
