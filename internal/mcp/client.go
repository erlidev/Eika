package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
)

// ElicitFunc asks the user what a server wants to know and returns the
// answer. It blocks until the user answers or ctx ends.
type ElicitFunc func(ctx context.Context, req ElicitRequest) (ElicitResult, error)

// Handlers say what a client does with what a server sends unasked. Either
// may be nil.
type Handlers struct {
	// Notify receives list changes and log messages.
	Notify func(method string, params json.RawMessage)
	// Closed reports that a connection over a shared channel ended on its
	// own: a stdio process exited or an event stream closed. It runs on the
	// transport's goroutine, so it must not block or close the client.
	Closed func(err error)
}

// HTTPTarget is a server reached over HTTP.
type HTTPTarget struct {
	URL     string
	Client  *http.Client
	Headers HeaderFunc
}

// Client is one connection to an MCP server, of whichever era it speaks. It
// is safe for concurrent use: every request is independent.
type Client struct {
	kind     string
	self     Implementation
	handlers Handlers

	// ctx lives as long as the client; a server request is answered on it.
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu sync.Mutex
	// t is set once, before the client is returned, but a transport may
	// deliver a server's request while the client is still being built.
	t            transport
	era          Era
	version      string
	server       Implementation
	capabilities ServerCapabilities
	instructions string
	supported    []string
	nextID       int64
	pending      map[string]*pendingCall
	closed       bool
	closeErr     error
}

// pendingCall is a request waiting for its response.
type pendingCall struct {
	resp chan *message
	lost chan error
	// progress and elicit belong to the call that set them: a progress
	// notification or a server's question is routed to the request it is
	// about.
	progress func(progressParams)
	elicit   ElicitFunc
}

// Info is what a connection learned about its server.
type Info struct {
	Era          Era
	Transport    string
	Version      string
	Server       Implementation
	Capabilities ServerCapabilities
	Instructions string
	// Supported lists the versions a modern server says it speaks.
	Supported []string
}

func newClient(self Implementation, kind string, handlers Handlers) *Client {
	ctx, stop := context.WithCancel(context.Background())
	return &Client{self: self, kind: kind, handlers: handlers, ctx: ctx, stop: stop, pending: map[string]*pendingCall{}}
}

// ConnectHTTP connects to a server over HTTP, finding which era it speaks:
// a modern request first, an initialize handshake when the answer is not a
// modern one, and the deprecated HTTP+SSE transport when the endpoint takes
// no POST at all.
func ConnectHTTP(ctx context.Context, target HTTPTarget, self Implementation, handlers Handlers) (*Client, error) {
	c := newClient(self, TransportStreamableHTTP, handlers)
	ht := newHTTPTransport(target.URL, target.Client, target.Headers, c.receiver())
	c.use(ht)
	probe := c.discover(ctx)
	if probe == nil {
		return c, nil
	}
	if !legacyAnswer(probe) {
		c.Close()
		return nil, probe
	}
	legacy := c.initialize(ctx)
	if legacy == nil {
		ht.becomeLegacy(c.Version())
		ht.listen()
		return c, nil
	}
	c.Close()
	var status *HTTPError
	if !errors.As(legacy, &status) || status.RPC != nil ||
		(status.Status != http.StatusBadRequest && status.Status != http.StatusNotFound && status.Status != http.StatusMethodNotAllowed) {
		return nil, legacy
	}
	// The endpoint takes no POST: it may be a 2024-11-05 server, whose GET
	// stream names where to post.
	c = newClient(self, TransportSSE, handlers)
	st, err := dialSSE(ctx, target.URL, target.Client, target.Headers, c.receiver())
	if err != nil {
		var auth *AuthError
		if errors.As(err, &auth) {
			return nil, err
		}
		return nil, fmt.Errorf("%w; as an HTTP+SSE server: %w", legacy, err)
	}
	c.use(st)
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// ConnectStdio connects to a server over a process's standard streams. A
// server that does not answer server/discover in time, or answers it with
// anything but a modern error, is taken for an initialize-based one.
func ConnectStdio(ctx context.Context, rw io.ReadWriteCloser, self Implementation, handlers Handlers) (*Client, error) {
	c := newClient(self, TransportStdio, handlers)
	st := newStdioTransport(rw, c.receiver())
	c.use(st)
	st.start()
	probeCtx, cancel := context.WithTimeout(ctx, stdioProbeTimeout)
	probe := c.discover(probeCtx)
	cancel()
	if probe == nil {
		return c, nil
	}
	timedOut := errors.Is(probe, context.DeadlineExceeded) && ctx.Err() == nil
	if !timedOut && !legacyAnswer(probe) {
		c.Close()
		return nil, probe
	}
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// legacyAnswer reports whether a failed server/discover probe came from a
// server that predates it. A recognised modern error, an authorization
// failure, or a server that cannot be reached at all is not one.
func legacyAnswer(err error) bool {
	var (
		rpc    *RPCError
		status *HTTPError
		auth   *AuthError
	)
	switch {
	case errors.As(err, &auth):
		return false
	case errors.As(err, &status):
		return status.RPC == nil || !status.RPC.modern()
	case errors.As(err, &rpc):
		return !rpc.modern()
	case errors.Is(err, errOnlyLegacy):
		return true
	}
	return false
}

// errOnlyLegacy reports a modern server that says it speaks only
// initialize-based versions to this client.
var errOnlyLegacy = errors.New("the server supports no modern version this client speaks")

// discover probes for a modern server and records what it says.
func (c *Client) discover(ctx context.Context) error {
	c.mu.Lock()
	c.era, c.version = EraModern, Version
	c.mu.Unlock()
	raw, err := c.roundTrip(ctx, "server/discover", map[string]any{}, callOpts{probe: true})
	if err != nil {
		var status *HTTPError
		if errors.As(err, &status) && status.RPC != nil {
			err = status.RPC
		}
		var rpc *RPCError
		if errors.As(err, &rpc) && rpc.Code == codeUnsupportedVersion {
			if supported := rpc.supportedVersions(); hasLegacy(supported) {
				return fmt.Errorf("%w (it lists %v)", errOnlyLegacy, supported)
			}
			return fmt.Errorf("the server speaks none of the protocol versions this client does: %w", rpc)
		}
		return err
	}
	var res discoverResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("decode server/discover result: %w", err)
	}
	var env resultEnvelope
	_ = json.Unmarshal(raw, &env)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.supported = res.SupportedVersions
	if len(res.SupportedVersions) == 0 {
		// Something answered, but not as a modern server would.
		return fmt.Errorf("%w (its answer to server/discover lists no versions)", errOnlyLegacy)
	}
	if !slices.Contains(res.SupportedVersions, Version) {
		if hasLegacy(res.SupportedVersions) {
			return fmt.Errorf("%w (it lists %v)", errOnlyLegacy, res.SupportedVersions)
		}
		return fmt.Errorf("the server speaks %v, none of which this client does", res.SupportedVersions)
	}
	c.capabilities, c.instructions = res.Capabilities, res.Instructions
	if raw, ok := env.Meta[metaServerInfo]; ok {
		_ = json.Unmarshal(raw, &c.server)
	}
	return nil
}

// hasLegacy reports whether versions holds an initialize-based revision this
// client speaks.
func hasLegacy(versions []string) bool {
	for _, v := range versions {
		if slices.Contains(legacyVersions, v) {
			return true
		}
	}
	return false
}

// initialize runs the handshake of an initialize-based server.
func (c *Client) initialize(ctx context.Context) error {
	c.mu.Lock()
	c.era, c.version = EraLegacy, ""
	c.mu.Unlock()
	raw, err := c.roundTrip(ctx, "initialize", initializeParams{
		ProtocolVersion: legacyVersions[0],
		Capabilities:    eikaCapabilities,
		ClientInfo:      c.self,
	}, callOpts{})
	if err != nil {
		return err
	}
	var res initializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("decode initialize result: %w", err)
	}
	if !slices.Contains(legacyVersions, res.ProtocolVersion) {
		return fmt.Errorf("the server speaks protocol version %q, which this client does not", res.ProtocolVersion)
	}
	c.mu.Lock()
	c.version, c.server, c.capabilities, c.instructions = res.ProtocolVersion, res.ServerInfo, res.Capabilities, res.Instructions
	c.mu.Unlock()
	m, err := notification(notifyInitialized, map[string]any{})
	if err != nil {
		return err
	}
	return c.transport().send(ctx, m, sendHeaders{version: res.ProtocolVersion, method: notifyInitialized})
}

// use sets the client's transport.
func (c *Client) use(t transport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// transport returns the client's transport.
func (c *Client) transport() transport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Info returns what the connection learned about its server.
func (c *Client) Info() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Info{
		Era:          c.era,
		Transport:    c.kind,
		Version:      c.version,
		Server:       c.server,
		Capabilities: c.capabilities,
		Instructions: c.instructions,
		Supported:    slices.Clone(c.supported),
	}
}

// Version returns the protocol version the connection speaks.
func (c *Client) Version() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// Close ends the connection. Requests still waiting fail.
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed, c.closeErr = true, errors.New("the connection is closed")
	t := c.t
	c.mu.Unlock()
	c.stop()
	if t != nil {
		_ = t.close()
	}
	c.wg.Wait()
}

// Done reports whether the connection ended, and why.
func (c *Client) Done() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed, c.closeErr
}

// callOpts are what one request adds to the plain exchange.
type callOpts struct {
	name     string
	headers  map[string]string
	progress func(progressParams)
	elicit   ElicitFunc
	// probe marks the server/discover probe, whose cancellation a legacy
	// server must not be sent: it has not been initialized.
	probe bool
}

// request sends one request and returns its complete result, answering the
// input a modern server asks for on the way.
func (c *Client) request(ctx context.Context, method string, params map[string]any, o callOpts) (json.RawMessage, error) {
	for round := 0; ; round++ {
		raw, err := c.roundTrip(ctx, method, params, o)
		if err != nil {
			return nil, err
		}
		var env resultEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode %s result: %w", method, err)
		}
		switch env.ResultType {
		case "", resultComplete:
			return raw, nil
		case resultInputRequired:
		default:
			return nil, fmt.Errorf("the server answered %s with result type %q, which this client does not know", method, env.ResultType)
		}
		if round+1 >= maxInputRounds {
			return nil, fmt.Errorf("the server asked for input %d times without finishing %s", maxInputRounds, method)
		}
		responses, err := c.answerInputs(ctx, env.InputRequests, o.elicit)
		if err != nil {
			return nil, err
		}
		// The retry is a new request carrying the answers and the server's
		// state, and nothing of an earlier round's.
		next := make(map[string]any, len(params)+2)
		for k, v := range params {
			if k != "inputResponses" && k != "requestState" {
				next[k] = v
			}
		}
		if len(responses) > 0 {
			next["inputResponses"] = responses
		}
		if env.RequestState != nil {
			next["requestState"] = *env.RequestState
		}
		params = next
	}
}

// answerInputs answers what a server asked for in an input_required result.
// Elicitation is the one kind of input Eika declares; a call that has nobody
// to ask cancels it, which a server must handle.
func (c *Client) answerInputs(ctx context.Context, requests map[string]inputRequest, elicit ElicitFunc) (map[string]any, error) {
	out := make(map[string]any, len(requests))
	for key, ir := range requests {
		if ir.Method != "elicitation/create" {
			return nil, fmt.Errorf("the server asked for %s, which Eika does not offer", ir.Method)
		}
		var req ElicitRequest
		if err := json.Unmarshal(ir.Params, &req); err != nil {
			return nil, fmt.Errorf("decode elicitation request: %w", err)
		}
		if elicit == nil {
			out[key] = ElicitResult{Action: ElicitCancel}
			continue
		}
		res, err := elicit(ctx, req)
		if err != nil {
			return nil, err
		}
		out[key] = res
	}
	return out, nil
}

// roundTrip sends one request and waits for its response.
func (c *Client) roundTrip(ctx context.Context, method string, params any, o callOpts) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return nil, err
	}
	c.nextID++
	id := requestID(c.nextID)
	key := idKey(id)
	t, era, version := c.t, c.era, c.version
	p := &pendingCall{resp: make(chan *message, 1), lost: make(chan error, 1), progress: o.progress, elicit: o.elicit}
	c.pending[key] = p
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()

	body, err := c.withMeta(params, era, version, id, o.progress != nil)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", method, err)
	}
	m := &message{JSONRPC: "2.0", ID: id, Method: method, Params: body}
	h := sendHeaders{version: version, modern: era == EraModern, method: method, name: o.name, params: o.headers}
	if err := t.send(ctx, m, h); err != nil {
		return nil, err
	}
	select {
	case r := <-p.resp:
		if r.Error != nil {
			return nil, r.Error
		}
		if r.Result == nil {
			return nil, fmt.Errorf("%s: the response has no result", method)
		}
		return r.Result, nil
	case err := <-p.lost:
		return nil, fmt.Errorf("%s: %w", method, err)
	case <-ctx.Done():
		if !o.probe {
			t.cancelled(ctx, key)
		}
		return nil, ctx.Err()
	}
}

// withMeta adds the _meta a request carries: on a modern connection the
// version, the client, and its capabilities; on either the progress token.
func (c *Client) withMeta(params any, era Era, version string, id json.RawMessage, progress bool) (json.RawMessage, error) {
	meta := map[string]any{}
	if era == EraModern {
		meta[metaProtocolVersion] = version
		meta[metaClientInfo] = c.self
		meta[metaClientCapabilities] = eikaCapabilities
	}
	if progress {
		// The request's own id is unique among the requests in flight, which
		// is all a progress token must be.
		meta[metaProgressToken] = id
	}
	data, err := json.Marshal(params)
	if err != nil || len(meta) == 0 {
		return data, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	if obj["_meta"], err = json.Marshal(meta); err != nil {
		return nil, err
	}
	return json.Marshal(obj)
}

// receiver is what the client's transport delivers to.
func (c *Client) receiver() receiver {
	return receiver{receive: c.receive, lost: c.lost, closed: c.closedBy}
}

// receive routes one message from the server.
func (c *Client) receive(m *message, via string) {
	switch {
	case m.isResponse():
		c.mu.Lock()
		p := c.pending[idKey(m.ID)]
		c.mu.Unlock()
		if p != nil {
			select {
			case p.resp <- m:
			default:
			}
		}
	case m.isNotification():
		c.notified(m)
	case m.isRequest():
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.answer(m, via)
		}()
	}
}

// notified handles a notification: progress goes to the request it is
// about, and everything else to the handlers.
func (c *Client) notified(m *message) {
	if m.Method == notifyProgress {
		var p progressParams
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		c.mu.Lock()
		call := c.pending[idKey(p.ProgressToken)]
		c.mu.Unlock()
		if call != nil && call.progress != nil {
			call.progress(p)
		}
		return
	}
	switch m.Method {
	case notifyCancelled, notifyAcknowledged:
		return
	}
	if c.handlers.Notify != nil {
		c.handlers.Notify(m.Method, m.Params)
	}
}

// answer responds to a request from an initialize-based server: a ping, or
// an elicitation for the call it is about. Anything else is a method Eika
// does not offer.
func (c *Client) answer(m *message, via string) {
	var (
		result any
		rpcErr *RPCError
	)
	switch m.Method {
	case "ping":
		result = map[string]any{}
	case "elicitation/create":
		var req ElicitRequest
		if err := json.Unmarshal(m.Params, &req); err != nil {
			rpcErr = &RPCError{Code: codeInvalidParams, Message: err.Error()}
			break
		}
		elicit := c.elicitorFor(via)
		if elicit == nil {
			result = ElicitResult{Action: ElicitCancel}
			break
		}
		res, err := elicit(c.ctx, req)
		if err != nil {
			rpcErr = &RPCError{Code: codeInternal, Message: err.Error()}
			break
		}
		result = res
	default:
		rpcErr = &RPCError{Code: codeMethodNotFound, Message: "Eika does not offer " + m.Method}
	}
	reply := &message{JSONRPC: "2.0", ID: m.ID, Error: rpcErr}
	if rpcErr == nil {
		data, err := json.Marshal(result)
		if err != nil {
			return
		}
		reply.Result = data
	}
	c.mu.Lock()
	t, version := c.t, c.version
	c.mu.Unlock()
	if t == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.ctx, requestTimeout)
	defer cancel()
	_ = t.send(ctx, reply, sendHeaders{version: version, method: m.Method})
}

// elicitorFor finds who asks the user for a server's request: the call whose
// stream carried it, or, on a channel every call shares, the one call in
// flight that can ask.
func (c *Client) elicitorFor(via string) ElicitFunc {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p := c.pending[via]; via != "" && p != nil {
		return p.elicit
	}
	var found ElicitFunc
	for _, p := range c.pending {
		if p.elicit == nil {
			continue
		}
		if found != nil {
			// Two calls could be the one it is about; asking about the wrong
			// one would be worse than not asking.
			return nil
		}
		found = p.elicit
	}
	return found
}

// lost fails a request whose response can no longer arrive.
func (c *Client) lost(id string, err error) {
	c.mu.Lock()
	p := c.pending[id]
	c.mu.Unlock()
	if p != nil {
		select {
		case p.lost <- err:
		default:
		}
	}
}

// closedBy ends the client when the channel every request shares ends.
func (c *Client) closedBy(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed, c.closeErr = true, err
	calls := make([]*pendingCall, 0, len(c.pending))
	for _, p := range c.pending {
		calls = append(calls, p)
	}
	c.mu.Unlock()
	for _, p := range calls {
		select {
		case p.lost <- err:
		default:
		}
	}
	if c.handlers.Closed != nil {
		c.handlers.Closed(err)
	}
}

// notification builds a notification message.
func notification(method string, params any) (*message, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", method, err)
	}
	return &message{JSONRPC: "2.0", Method: method, Params: data}, nil
}
