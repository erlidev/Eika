package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
