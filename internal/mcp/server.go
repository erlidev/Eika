package mcp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/mcp/oauth"
)

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
