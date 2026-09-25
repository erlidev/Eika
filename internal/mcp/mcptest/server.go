package mcptest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// The eras a Server speaks, and so the transport it offers over HTTP.
const (
	// Modern is the 2026-07-28 revision: stateless POSTs, server/discover.
	Modern = "modern"
	// Legacy is Streamable HTTP of 2025-11-25: initialize, a session id, a
	// GET stream.
	Legacy = "legacy"
	// SSE is the HTTP+SSE transport of 2024-11-05.
	SSE = "sse"
)

// Tool is a tool the server offers and what calling it does.
type Tool struct {
	// Definition is the tool as tools/list reports it.
	Definition json.RawMessage
	// Call answers a call with its arguments. It returns the result object
	// (content, isError, ...) or a JSON-RPC error.
	Call func(ctx context.Context, call *Call) (any, *Error)
}

// Call is one tools/call the server received.
type Call struct {
	Name      string
	Arguments json.RawMessage
	// Headers are the HTTP headers the request carried.
	Headers http.Header
	// InputResponses and RequestState are what a multi round-trip retry
	// carried back.
	InputResponses map[string]json.RawMessage
	RequestState   string
	// Progress sends a progress notification on the call's stream.
	Progress func(progress, total float64, message string)
	// Elicit asks the client, as an initialize-based server does, with a
	// request of its own. Only a legacy server has it.
	Elicit func(params any) (json.RawMessage, error)
}

// Error is a JSON-RPC error.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Server is a scripted MCP server for tests. Set its fields before serving.
type Server struct {
	// Era is Modern, Legacy, or SSE.
	Era   string
	Name  string
	Tools []Tool
	// Resources, Templates, and Prompts are what the lists report.
	Resources []json.RawMessage
	Templates []json.RawMessage
	Prompts   []json.RawMessage
	// ReadResource answers resources/read; GetPrompt answers prompts/get.
	ReadResource func(uri string) (any, *Error)
	GetPrompt    func(name string, args map[string]string) (any, *Error)
	// PageSize splits tools/list into pages of this many.
	PageSize int
	// Stream answers every request as an event stream.
	Stream bool
	// Instructions are what the server tells the model.
	Instructions string
	// Authorize, when set, decides whether a request's Authorization header
	// is good; a request it refuses gets 401 with Challenge.
	Authorize func(header string) bool
	Challenge string

	mu       sync.Mutex
	sessions map[string]*session
	// Requests are the JSON-RPC methods the server received, in order.
	requests []Request
	// changes wakes every subscription and GET stream when a list changes.
	changes chan struct{}
}

// Request is one message the server received and the headers it carried.
type Request struct {
	Method  string
	Headers http.Header
	Params  json.RawMessage
}

// session is a legacy connection's state.
type session struct {
	id string
	// out carries what the server sends on the GET stream or, for SSE, on
	// the one stream.
	out chan []byte
	// replies are the client's answers to the server's own requests.
	replies map[string]chan json.RawMessage
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Requests returns the requests the server has received.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Methods returns the methods the server has received, in order.
func (s *Server) Methods() []string {
	var out []string
	for _, r := range s.Requests() {
		out = append(out, r.Method)
	}
	return out
}

// ListChanged tells every open subscription and legacy stream that the tool
// list changed.
func (s *Server) ListChanged() {
	s.mu.Lock()
	ch := s.changes
	s.changes = make(chan struct{})
	s.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (s *Server) changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changes == nil {
		s.changes = make(chan struct{})
	}
	return s.changes
}

func (s *Server) record(m *message, h http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{Method: m.Method, Headers: h.Clone(), Params: m.Params})
}

// ServeHTTP serves the server's era over HTTP.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Authorize != nil && !s.Authorize(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", s.Challenge)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch s.Era {
	case SSE:
		s.serveSSE(w, r)
	case Legacy:
		s.serveLegacy(w, r)
	default:
		s.serveModern(w, r)
	}
}

// serveModern is the 2026-07-28 Streamable HTTP endpoint.
func (s *Server) serveModern(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	m, ok := readMessage(w, r)
	if !ok {
		return
	}
	s.record(m, r.Header)
	if m.Method == "initialize" {
		writeError(w, http.StatusBadRequest, m.ID, &Error{Code: -32601, Message: "this server supports only 2026-07-28"})
		return
	}
	var meta struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	_ = json.Unmarshal(m.Params, &meta)
	var version string
	_ = json.Unmarshal(meta.Meta["io.modelcontextprotocol/protocolVersion"], &version)
	switch {
	case version == "":
		writeError(w, http.StatusBadRequest, m.ID, &Error{Code: -32602, Message: "missing _meta protocol version"})
		return
	case version != "2026-07-28":
		data, _ := json.Marshal(map[string]any{"supported": []string{"2026-07-28"}, "requested": version})
		writeError(w, http.StatusBadRequest, m.ID, &Error{Code: -32022, Message: "Unsupported protocol version", Data: data})
		return
	case r.Header.Get("MCP-Protocol-Version") != version || r.Header.Get("Mcp-Method") != m.Method:
		writeError(w, http.StatusBadRequest, m.ID, &Error{Code: -32020, Message: "header mismatch"})
		return
	}
	if m.Method == "subscriptions/listen" {
		s.listen(w, r, m)
		return
	}
	s.answer(w, r, m, nil)
}

// answer runs a request and writes its response, as JSON or as a stream
// carrying its progress first.
func (s *Server) answer(w http.ResponseWriter, r *http.Request, m *message, elicit func(any) (json.RawMessage, error)) {
	var notes [][]byte
	var mu sync.Mutex
	progress := func(token json.RawMessage) func(float64, float64, string) {
		return func(p, total float64, text string) {
			note, _ := json.Marshal(message{JSONRPC: "2.0", Method: "notifications/progress", Params: mustJSON(map[string]any{
				"progressToken": token, "progress": p, "total": total, "message": text,
			})})
			mu.Lock()
			notes = append(notes, note)
			mu.Unlock()
		}
	}
	result, rpcErr := s.handle(r.Context(), m, r.Header, progress, elicit)
	reply := message{JSONRPC: "2.0", ID: m.ID, Error: rpcErr}
	if rpcErr == nil {
		reply.Result = mustJSON(result)
	}
	data, _ := json.Marshal(reply)
	if !s.Stream && len(notes) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, note := range notes {
		writeEvent(w, "message", note)
	}
	writeEvent(w, "message", data)
}

// handle runs one request.
func (s *Server) handle(ctx context.Context, m *message, h http.Header, progress func(json.RawMessage) func(float64, float64, string), elicit func(any) (json.RawMessage, error)) (any, *Error) {
	var params struct {
		Name           string                     `json:"name"`
		URI            string                     `json:"uri"`
		Arguments      json.RawMessage            `json:"arguments"`
		Cursor         string                     `json:"cursor"`
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
		RequestState   string                     `json:"requestState"`
		Meta           struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	_ = json.Unmarshal(m.Params, &params)
	caps := map[string]any{"tools": map[string]any{"listChanged": true}}
	if len(s.Resources) > 0 || len(s.Templates) > 0 {
		caps["resources"] = map[string]any{"listChanged": true}
	}
	if len(s.Prompts) > 0 {
		caps["prompts"] = map[string]any{}
	}
	info := map[string]any{"name": s.name(), "version": "1.2.3"}
	switch m.Method {
	case "server/discover":
		return map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{"2026-07-28"},
			"capabilities":      caps,
			"instructions":      s.Instructions,
			"_meta":             map[string]any{"io.modelcontextprotocol/serverInfo": info},
			"ttlMs":             0,
			"cacheScope":        "public",
		}, nil
	case "initialize":
		return map[string]any{"protocolVersion": "2025-11-25", "capabilities": caps, "serverInfo": info, "instructions": s.Instructions}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		defs := make([]json.RawMessage, 0, len(s.Tools))
		for _, t := range s.Tools {
			defs = append(defs, t.Definition)
		}
		start := 0
		if params.Cursor != "" {
			fmt.Sscan(params.Cursor, &start)
		}
		out := map[string]any{"resultType": "complete", "ttlMs": 0, "cacheScope": "public"}
		end := len(defs)
		if s.PageSize > 0 && start+s.PageSize < len(defs) {
			end = start + s.PageSize
			out["nextCursor"] = fmt.Sprint(end)
		}
		out["tools"] = defs[min(start, len(defs)):end]
		return out, nil
	case "resources/list":
		return map[string]any{"resources": nonNil(s.Resources)}, nil
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": nonNil(s.Templates)}, nil
	case "prompts/list":
		return map[string]any{"prompts": nonNil(s.Prompts)}, nil
	case "resources/read":
		if s.ReadResource == nil {
			return nil, &Error{Code: -32602, Message: "no such resource"}
		}
		return s.ReadResource(params.URI)
	case "prompts/get":
		if s.GetPrompt == nil {
			return nil, &Error{Code: -32602, Message: "no such prompt"}
		}
		var args struct {
			Arguments map[string]string `json:"arguments"`
		}
		_ = json.Unmarshal(m.Params, &args)
		return s.GetPrompt(params.Name, args.Arguments)
	case "tools/call":
		for _, t := range s.Tools {
			var def struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(t.Definition, &def)
			if def.Name != params.Name {
				continue
			}
			call := &Call{
				Name:           params.Name,
				Arguments:      params.Arguments,
				Headers:        h,
				InputResponses: params.InputResponses,
				RequestState:   params.RequestState,
				Progress:       func(float64, float64, string) {},
				Elicit:         elicit,
			}
			if len(params.Meta.ProgressToken) > 0 {
				call.Progress = progress(params.Meta.ProgressToken)
			}
			return t.Call(ctx, call)
		}
		return nil, &Error{Code: -32602, Message: "Unknown tool: " + params.Name}
	}
	return nil, &Error{Code: -32601, Message: "Method not found: " + m.Method}
}

// listen holds a subscription open, sending a list change whenever the
// test says one happened.
func (s *Server) listen(w http.ResponseWriter, r *http.Request, m *message) {
	w.Header().Set("Content-Type", "text/event-stream")
	ack, _ := json.Marshal(message{JSONRPC: "2.0", Method: "notifications/subscriptions/acknowledged",
		Params: mustJSON(map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/subscriptionId": m.ID}, "notifications": map[string]any{"toolsListChanged": true}})})
	writeEvent(w, "message", ack)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.changed():
			note, _ := json.Marshal(message{JSONRPC: "2.0", Method: "notifications/tools/list_changed",
				Params: mustJSON(map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/subscriptionId": m.ID}})})
			writeEvent(w, "message", note)
		}
	}
}

// serveLegacy is the 2025-11-25 Streamable HTTP endpoint.
func (s *Server) serveLegacy(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sess := s.session(r.Header.Get("Mcp-Session-Id"))
		if sess == nil {
			http.Error(w, "no session", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		s.pump(w, r, sess)
		return
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.sessions, r.Header.Get("Mcp-Session-Id"))
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	case http.MethodPost:
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	m, ok := readMessage(w, r)
	if !ok {
		return
	}
	s.record(m, r.Header)
	if m.Method == "" {
		// A response to one of the server's own requests.
		s.deliver(r.Header.Get("Mcp-Session-Id"), m)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if m.Method == "initialize" {
		sess := s.newSession()
		w.Header().Set("Mcp-Session-Id", sess.id)
		s.answer(w, r, m, nil)
		return
	}
	sess := s.session(r.Header.Get("Mcp-Session-Id"))
	if sess == nil {
		// Before initialize there is no session to find, which is how an
		// older server refuses a modern probe.
		if r.Header.Get("Mcp-Session-Id") == "" {
			http.Error(w, "Bad Request: Server not initialized", http.StatusBadRequest)
		} else {
			http.Error(w, "session not found", http.StatusNotFound)
		}
		return
	}
	if len(m.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	s.answerLegacy(w, r, m, sess)
}

// answerLegacy answers on a stream of its own, so the tool may send its own
// requests on it.
func (s *Server) answerLegacy(w http.ResponseWriter, r *http.Request, m *message, sess *session) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	var mu sync.Mutex
	send := func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		writeEvent(w, "message", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	elicit := func(params any) (json.RawMessage, error) {
		id := randomID()
		reply := make(chan json.RawMessage, 1)
		s.mu.Lock()
		sess.replies[id] = reply
		s.mu.Unlock()
		req, _ := json.Marshal(message{JSONRPC: "2.0", ID: mustJSON(id), Method: "elicitation/create", Params: mustJSON(params)})
		send(req)
		select {
		case res := <-reply:
			return res, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("the client did not answer")
		}
	}
	progress := func(token json.RawMessage) func(float64, float64, string) {
		return func(p, total float64, text string) {
			note, _ := json.Marshal(message{JSONRPC: "2.0", Method: "notifications/progress", Params: mustJSON(map[string]any{
				"progressToken": token, "progress": p, "total": total, "message": text,
			})})
			send(note)
		}
	}
	result, rpcErr := s.handle(r.Context(), m, r.Header, progress, elicit)
	reply := message{JSONRPC: "2.0", ID: m.ID, Error: rpcErr}
	if rpcErr == nil {
		reply.Result = mustJSON(result)
	}
	data, _ := json.Marshal(reply)
	send(data)
}

// serveSSE is the 2024-11-05 HTTP+SSE transport: the stream at "/", and
// the endpoint it names at "/messages".
func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet:
		sess := s.newSession()
		w.Header().Set("Content-Type", "text/event-stream")
		base := strings.TrimSuffix(r.URL.Path, "/")
		writeEvent(w, "endpoint", []byte(base+"/messages?sessionId="+sess.id))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		s.pump(w, r, sess)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
		sess := s.session(r.URL.Query().Get("sessionId"))
		if sess == nil {
			http.Error(w, "no session", http.StatusNotFound)
			return
		}
		m, ok := readMessage(w, r)
		if !ok {
			return
		}
		s.record(m, r.Header)
		w.WriteHeader(http.StatusAccepted)
		if len(m.ID) == 0 || m.Method == "" {
			return
		}
		result, rpcErr := s.handle(context.Background(), m, r.Header, func(json.RawMessage) func(float64, float64, string) {
			return func(float64, float64, string) {}
		}, nil)
		reply := message{JSONRPC: "2.0", ID: m.ID, Error: rpcErr}
		if rpcErr == nil {
			reply.Result = mustJSON(result)
		}
		data, _ := json.Marshal(reply)
		sess.out <- data
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// pump writes a session's messages to its stream until the client leaves.
func (s *Server) pump(w http.ResponseWriter, r *http.Request, sess *session) {
	flusher, _ := w.(http.Flusher)
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-sess.out:
			writeEvent(w, "message", data)
		case <-s.changed():
			note, _ := json.Marshal(message{JSONRPC: "2.0", Method: "notifications/tools/list_changed"})
			writeEvent(w, "message", note)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (s *Server) newSession() *session {
	sess := &session{id: randomID(), out: make(chan []byte, 16), replies: map[string]chan json.RawMessage{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]*session{}
	}
	s.sessions[sess.id] = sess
	return sess
}

func (s *Server) session(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

// deliver hands the client's answer to the request that waits for it.
func (s *Server) deliver(sessionID string, m *message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[sessionID]
	if sess == nil {
		return
	}
	var id string
	_ = json.Unmarshal(m.ID, &id)
	if reply, ok := sess.replies[id]; ok {
		reply <- m.Result
		delete(sess.replies, id)
	}
}

// ExpireSessions forgets every legacy session, as a restarted server does.
func (s *Server) ExpireSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = nil
}

func (s *Server) name() string {
	if s.Name != "" {
		return s.Name
	}
	return "scripted"
}

// ServeStdio serves the server's era over a process's standard streams:
// Modern answers server/discover, Legacy refuses it and wants initialize.
// It returns when in ends.
func (s *Server) ServeStdio(in io.Reader, out io.Writer) {
	var mu sync.Mutex
	write := func(v any) {
		data, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(append(data, '\n'))
	}
	pending := map[string]chan json.RawMessage{}
	var pendingMu sync.Mutex
	var wg sync.WaitGroup
	defer wg.Wait()
	scan := bufio.NewScanner(in)
	scan.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for scan.Scan() {
		var m message
		if json.Unmarshal(bytes.TrimSpace(scan.Bytes()), &m) != nil {
			continue
		}
		s.record(&m, http.Header{})
		if m.Method == "" {
			var id string
			_ = json.Unmarshal(m.ID, &id)
			pendingMu.Lock()
			if reply, ok := pending[id]; ok {
				reply <- m.Result
				delete(pending, id)
			}
			pendingMu.Unlock()
			continue
		}
		if len(m.ID) == 0 {
			continue
		}
		if s.Era == Legacy && m.Method == "server/discover" {
			write(message{JSONRPC: "2.0", ID: m.ID, Error: &Error{Code: -32601, Message: "Method not found"}})
			continue
		}
		wg.Add(1)
		go func(m message) {
			defer wg.Done()
			progress := func(token json.RawMessage) func(float64, float64, string) {
				return func(p, total float64, text string) {
					write(message{JSONRPC: "2.0", Method: "notifications/progress", Params: mustJSON(map[string]any{
						"progressToken": token, "progress": p, "total": total, "message": text,
					})})
				}
			}
			var elicit func(any) (json.RawMessage, error)
			if s.Era == Legacy {
				elicit = func(params any) (json.RawMessage, error) {
					id := randomID()
					reply := make(chan json.RawMessage, 1)
					pendingMu.Lock()
					pending[id] = reply
					pendingMu.Unlock()
					write(message{JSONRPC: "2.0", ID: mustJSON(id), Method: "elicitation/create", Params: mustJSON(params)})
					select {
					case res := <-reply:
						return res, nil
					case <-time.After(10 * time.Second):
						return nil, fmt.Errorf("the client did not answer")
					}
				}
			}
			result, rpcErr := s.handle(context.Background(), &m, http.Header{}, progress, elicit)
			reply := message{JSONRPC: "2.0", ID: m.ID, Error: rpcErr}
			if rpcErr == nil {
				reply.Result = mustJSON(result)
			}
			write(reply)
		}(m)
	}
}

// Pipe returns the client's end of a stdio connection to the server, which
// serves it until the end is closed.
func (s *Server) Pipe() io.ReadWriteCloser {
	toServer, fromClient := io.Pipe()
	toClient, fromServer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.ServeStdio(toServer, fromServer)
		_ = fromServer.Close()
	}()
	return &pipe{r: toClient, w: fromClient, done: done}
}

type pipe struct {
	r    *io.PipeReader
	w    *io.PipeWriter
	done chan struct{}
}

func (p *pipe) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipe) Write(b []byte) (int, error) { return p.w.Write(b) }

// Close ends the connection, as a process exiting does.
func (p *pipe) Close() error {
	_ = p.w.Close()
	_ = p.r.Close()
	<-p.done
	return nil
}

func readMessage(w http.ResponseWriter, r *http.Request) (*message, bool) {
	var m message
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return nil, false
	}
	return &m, true
}

func writeError(w http.ResponseWriter, status int, id json.RawMessage, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(message{JSONRPC: "2.0", ID: id, Error: e})
}

func writeEvent(w io.Writer, name string, data []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func nonNil(list []json.RawMessage) []json.RawMessage {
	if list == nil {
		return []json.RawMessage{}
	}
	return list
}

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Text is a tool result holding one text block.
func Text(text string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
}

// Image is a content block holding an image.
func Image(mime string, data []byte) map[string]any {
	return map[string]any{"type": "image", "mimeType": mime, "data": base64.StdEncoding.EncodeToString(data)}
}

// ToolDef is a tool definition with an input schema.
func ToolDef(name, description, schema string) json.RawMessage {
	return mustJSON(map[string]any{"name": name, "description": description, "inputSchema": json.RawMessage(schema)})
}
