package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/erlidev/eika/internal/tool"
)

// ToolPrefix begins the name of every tool a server offers the model.
const ToolPrefix = "mcp__"

// maxToolName is the longest function name Chat Completions accepts.
const maxToolName = 64

// The tools that read servers' resources, offered when a server in the run
// has any.
const (
	listResourcesTool = "mcp_list_resources"
	readResourceTool  = "mcp_read_resource"
)

// ToolNames returns the names the model calls a server's tools by, in the
// order given: mcp__<server>__<tool>, with the characters a function name
// may not hold replaced, and cut to 64 characters with a hash of the
// original name when it is longer or the replacing made two alike.
func ToolNames(server string, tools []Tool) []string {
	taken := map[string]bool{}
	out := make([]string, len(tools))
	for i, t := range tools {
		base := ToolPrefix + functionName(server) + "__" + functionName(t.Name)
		name := base
		if len(name) > maxToolName || taken[name] {
			sum := sha256.Sum256([]byte(t.Name))
			name = base[:min(len(base), maxToolName-7)] + "_" + hex.EncodeToString(sum[:3])
		}
		taken[name] = true
		out[i] = name
	}
	return out
}

// functionName replaces what a function name may not hold.
func functionName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

// serverTool is one tool of one server, as a run offers it.
type serverTool struct {
	pool     *Pool
	serverID string
	server   string
	name     string
	tool     Tool
	headers  []headerParam
	// local marks a stdio server's tool, which runs in the call's
	// workspace.
	local bool
}

// Name is the name the model calls the tool by.
func (t *serverTool) Name() string { return t.name }

// Description is the server's, or the tool's title when it gave none.
func (t *serverTool) Description() string {
	if d := strings.TrimSpace(t.tool.Description); d != "" {
		return d
	}
	return fmt.Sprintf("%s, from the MCP server %s.", orName(t.tool.Title, t.tool.Name), t.server)
}

// Schema is the server's input schema, made into what every endpoint
// accepts: an object schema with properties and no $schema.
func (t *serverTool) Schema() json.RawMessage { return toolSchema(t.tool.InputSchema) }

// Call calls the tool on its server.
func (t *serverTool) Call(ctx context.Context, c tool.CallContext, args json.RawMessage) (tool.Result, error) {
	return t.pool.call(ctx, t, c, args), nil
}

// remoteTool is a remote server's tool, which a chat may offer: the call is
// a network request the harness makes.
type remoteTool struct{ *serverTool }

// Standalone marks the tool as one that needs no workspace.
func (remoteTool) Standalone() {}

var _ tool.Standalone = remoteTool{}

// toolSchema normalizes an input schema for a model.
func toolSchema(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	delete(obj, "$schema")
	if _, ok := obj["type"]; !ok {
		obj["type"] = json.RawMessage(`"object"`)
	}
	if _, ok := obj["properties"]; !ok {
		obj["properties"] = json.RawMessage(`{}`)
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return data
}

// Tools returns the tools a run may offer from every server it can reach:
// the remote servers, and the stdio servers in workspaceID when there is
// one. A server not connected yet is given connectWait to connect; one that
// cannot be reached leaves its tools out.
func (p *Pool) Tools(ctx context.Context, workspaceID string) []tool.Tool {
	cfgs, err := p.opts.Store.MCPServers(ctx)
	if err != nil {
		p.log.Error("list mcp servers for a run", "error", err)
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, connectWait)
	defer cancel()
	var (
		mu    sync.Mutex
		found []listedServer
		wg    sync.WaitGroup
	)
	for _, cfg := range cfgs {
		if !cfg.Enabled || (cfg.Kind == KindStdio && workspaceID == "") {
			continue
		}
		wg.Go(func() {
			c, err := p.connection(waitCtx, cfg.ID, workspaceID, false)
			if err != nil {
				return
			}
			lists := p.listsOf(cfg.ID, c)
			mu.Lock()
			found = append(found, listedServer{cfg, lists})
			mu.Unlock()
		})
	}
	wg.Wait()
	return p.offer(found)
}

// Offered returns the tools each enabled server offered when it was last
// listed, and the resource tools when one of them has resources, without
// connecting to any or reading the store: what the tool list and a
// session's tool choice show. It knows the servers Start, a connection, or
// Changed brought it.
func (p *Pool) Offered() []tool.Tool {
	p.mu.Lock()
	servers := make([]*server, 0, len(p.servers))
	for _, s := range p.servers {
		servers = append(servers, s)
	}
	p.mu.Unlock()
	var found []listedServer
	for _, s := range servers {
		s.mu.Lock()
		cfg := ServerConfig{ID: s.id, Name: s.name, Kind: s.kind, Enabled: s.enabled, DisabledTools: s.disabled}
		lists := s.catalog
		s.mu.Unlock()
		if cfg.Enabled {
			found = append(found, listedServer{cfg, lists})
		}
	}
	return p.offer(found)
}

// listedServer is a server's configuration and what it listed.
type listedServer struct {
	cfg   ServerConfig
	lists Catalog
}

// offer turns what servers listed into the tools a run offers, by server
// name, with the resource tools when one of the servers has resources.
func (p *Pool) offer(found []listedServer) []tool.Tool {
	slices.SortFunc(found, func(a, b listedServer) int { return strings.Compare(a.cfg.Name, b.cfg.Name) })
	var (
		out       []tool.Tool
		resources []resourceServer
	)
	for _, l := range found {
		out = append(out, p.serverTools(l.cfg, l.lists.Tools)...)
		if len(l.lists.Resources) > 0 || len(l.lists.Templates) > 0 {
			resources = append(resources, resourceServer{id: l.cfg.ID, name: l.cfg.Name, local: l.cfg.Kind == KindStdio})
		}
	}
	if len(resources) > 0 {
		out = append(out, listResources{pool: p, servers: resources}, readResource{pool: p, servers: resources})
	}
	return out
}

// ServerOf names the MCP server a tool calls, empty for any other tool,
// including the resource tools, which reach every server of the run.
func ServerOf(t tool.Tool) string {
	switch st := t.(type) {
	case *serverTool:
		return st.server
	case remoteTool:
		return st.server
	}
	return ""
}

// serverTools turns a server's listed tools into the tools a run offers,
// leaving out those the user turned off.
func (p *Pool) serverTools(cfg ServerConfig, tools []Tool) []tool.Tool {
	names := ToolNames(cfg.Name, tools)
	out := make([]tool.Tool, 0, len(tools))
	for i, t := range tools {
		if slices.Contains(cfg.DisabledTools, t.Name) {
			continue
		}
		params, _ := headerParams(t.InputSchema)
		st := &serverTool{pool: p, serverID: cfg.ID, server: cfg.Name, name: names[i], tool: t, headers: params, local: cfg.Kind == KindStdio}
		if st.local {
			out = append(out, st)
		} else {
			out = append(out, remoteTool{st})
		}
	}
	return out
}

// call runs one tool call. A server that is down, refuses, or fails is the
// model's to hear about, not a failure of the run.
func (p *Pool) call(ctx context.Context, t *serverTool, cc tool.CallContext, args json.RawMessage) tool.Result {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	workspace := ""
	if t.local {
		workspace = cc.WorkspaceID
	}
	opts := CallOptions{
		Headers:  paramHeaders(t.headers, args),
		Progress: func(pr Progress) { cc.Output(ctx, progressLine(pr)) },
		Elicit:   p.elicitor(cc, t.server),
	}
	for attempt := 0; ; attempt++ {
		c, err := p.connection(ctx, t.serverID, workspace, attempt > 0)
		if err != nil {
			return tool.Errorf("The MCP server %s is not available: %v", t.server, err)
		}
		res, err := c.client.CallTool(ctx, t.tool.Name, args, opts)
		if err == nil {
			details, _ := json.Marshal(resultDetails(t.server, t.tool.Name, res))
			return tool.Result{Content: resultText(res), IsError: res.IsError, Details: details}
		}
		if attempt == 0 && p.recover(ctx, t.serverID, c, err) {
			continue
		}
		return callFailed(t.server, err)
	}
}

// recover acts on a failed request and reports whether it may be sent
// again: only when the server certainly did not run it, because its
// session or its token had expired.
func (p *Pool) recover(ctx context.Context, id string, c *conn, err error) bool {
	p.mu.Lock()
	s := p.servers[id]
	p.mu.Unlock()
	if s == nil {
		return false
	}
	var auth *AuthError
	switch {
	case errors.Is(err, ErrSessionExpired):
		s.addLog("eika", "warning", "the server ended the session; connecting again")
		p.retire(s, c, err)
		return true
	case errors.As(err, &auth):
		s.mu.Lock()
		challenge := auth.Challenge
		s.challenge = &challenge
		s.mu.Unlock()
		if p.refreshAfter(ctx, s, auth) {
			return true
		}
		if auth.Status == http.StatusUnauthorized {
			p.retire(s, c, err)
		} else {
			s.addLog("eika", "warning", err.Error())
			p.emit(s)
		}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
	default:
		var (
			rpc    *RPCError
			status *HTTPError
		)
		if !errors.As(err, &rpc) && !errors.As(err, &status) {
			// The transport failed: the next call connects anew.
			p.retire(s, c, err)
		}
	}
	return false
}

// retire drops a connection that stopped working, so the next request
// connects again, and reports why.
func (p *Pool) retire(s *server, c *conn, err error) {
	s.mu.Lock()
	current := s.connFor(c.workspace) == c
	if current {
		failed := &conn{workspace: c.workspace, ready: make(chan struct{}), cancelOpen: func() {}, err: err, attempted: c.attempted}
		close(failed.ready)
		s.setConn(c.workspace, failed)
	}
	s.mu.Unlock()
	if current {
		c.close()
		p.emit(s)
	}
}

// callFailed words a failed call for the model.
func callFailed(server string, err error) tool.Result {
	var (
		rpc  *RPCError
		auth *AuthError
	)
	switch {
	case errors.As(err, &rpc):
		return tool.Errorf("The MCP server %s answered with an error: %s", server, rpc.Message)
	case errors.As(err, &auth) && auth.Status == http.StatusForbidden:
		return tool.Errorf("The MCP server %s refused the call: it needs more access (%s). The user can grant it by authorizing the server again under Settings, MCP.", server, orName(auth.Challenge.Scope, "no scope named"))
	case errors.As(err, &auth):
		return tool.Errorf("The MCP server %s needs authorization. The user can authorize it under Settings, MCP.", server)
	case errors.Is(err, context.DeadlineExceeded):
		return tool.Errorf("The MCP server %s did not answer within %s.", server, callTimeout)
	}
	return tool.Errorf("The call to the MCP server %s failed: %v", server, err)
}

// elicitor asks the session's user what a server wants to know.
func (p *Pool) elicitor(cc tool.CallContext, server string) ElicitFunc {
	if cc.SessionID == "" {
		return nil
	}
	return func(ctx context.Context, req ElicitRequest) (ElicitResult, error) {
		return p.opts.Elicitations.ask(ctx, Elicitation{SessionID: cc.SessionID, RunID: cc.RunID, CallID: cc.CallID, Server: server}, req)
	}
}

// progressLine renders a progress report as a line of tool output.
func progressLine(pr Progress) string {
	line := fmt.Sprintf("progress %g", pr.Progress)
	if pr.Total != nil && *pr.Total > 0 {
		line = fmt.Sprintf("progress %g/%g", pr.Progress, *pr.Total)
	}
	if pr.Message != "" {
		line += ": " + pr.Message
	}
	return line + "\n"
}

// ReadResource reads a resource of a server, in the given workspace for a
// stdio server. A server that asks the user for input is told there is
// nobody to ask.
func (p *Pool) ReadResource(ctx context.Context, id, workspaceID, uri string) (*ReadResourceResult, error) {
	c, err := p.connection(ctx, id, workspaceID, false)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return c.client.ReadResource(ctx, uri, nil)
}

// GetPrompt renders a prompt of a server with its arguments.
func (p *Pool) GetPrompt(ctx context.Context, id, workspaceID, name string, args map[string]string) (*GetPromptResult, error) {
	c, err := p.connection(ctx, id, workspaceID, false)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return c.client.GetPrompt(ctx, name, args, nil)
}

// resourceServer is a server whose resources the resource tools reach.
type resourceServer struct {
	id    string
	name  string
	local bool
}

// listResources lists the resources of the run's servers.
type listResources struct {
	pool    *Pool
	servers []resourceServer
}

// Name is the identifier the model calls the tool by.
func (listResources) Name() string { return listResourcesTool }

// Standalone marks the tool as one a chat may offer; a stdio server's
// resources are left out there.
func (listResources) Standalone() {}

// Description tells the model what the tool does.
func (t listResources) Description() string {
	return "List the resources (documents, files, records) the connected MCP servers offer, with their URIs and the URI templates that address more. " +
		"Read one with " + readResourceTool + ". Servers: " + t.names() + "."
}

func (t listResources) names() string {
	names := make([]string, 0, len(t.servers))
	for _, s := range t.servers {
		names = append(names, s.name)
	}
	return strings.Join(names, ", ")
}

// Schema describes the parameters of a call.
func (listResources) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"server":{"type":"string","description":"List only this server's resources."}},"additionalProperties":false}`)
}

// Call lists the resources the servers held when they were last listed.
func (t listResources) Call(ctx context.Context, cc tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	var args struct {
		Server string `json:"server"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &args) != nil {
		return tool.Errorf("%s: the arguments are not an object with an optional server", listResourcesTool), nil
	}
	var b strings.Builder
	for _, rs := range t.servers {
		if args.Server != "" && rs.name != args.Server {
			continue
		}
		c, err := t.pool.connection(ctx, rs.id, workspaceFor(rs, cc), false)
		if err != nil {
			fmt.Fprintf(&b, "## %s\n(not available: %v)\n\n", rs.name, err)
			continue
		}
		lists := t.pool.listsOf(rs.id, c)
		fmt.Fprintf(&b, "## %s\n", rs.name)
		for _, r := range lists.Resources {
			fmt.Fprintf(&b, "- %s: %s", r.URI, orName(r.Title, r.Name))
			if r.MimeType != "" {
				fmt.Fprintf(&b, " (%s)", r.MimeType)
			}
			if r.Description != "" {
				fmt.Fprintf(&b, " — %s", r.Description)
			}
			b.WriteByte('\n')
		}
		for _, r := range lists.Templates {
			fmt.Fprintf(&b, "- template %s: %s", r.URITemplate, orName(r.Title, r.Name))
			if r.Description != "" {
				fmt.Fprintf(&b, " — %s", r.Description)
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return tool.Errorf("no connected MCP server is called %q; the servers are %s", args.Server, t.names()), nil
	}
	return tool.Text(truncateMiddle(strings.TrimSpace(b.String()), maxResultBytes)), nil
}

// readResource reads one resource of one of the run's servers.
type readResource struct {
	pool    *Pool
	servers []resourceServer
}

// Name is the identifier the model calls the tool by.
func (readResource) Name() string { return readResourceTool }

// Standalone marks the tool as one a chat may offer.
func (readResource) Standalone() {}

// Description tells the model what the tool does.
func (readResource) Description() string {
	return "Read one resource of a connected MCP server by its URI, as " + listResourcesTool + " lists them or a template fills in."
}

// Schema describes the parameters of a call.
func (readResource) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"server":{"type":"string","description":"The MCP server's name."},"uri":{"type":"string","description":"The resource's URI."}},"required":["server","uri"],"additionalProperties":false}`)
}

// Call reads the resource.
func (t readResource) Call(ctx context.Context, cc tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	var args struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	if json.Unmarshal(raw, &args) != nil || args.Server == "" || args.URI == "" {
		return tool.Errorf("%s needs a server and a uri", readResourceTool), nil
	}
	i := slices.IndexFunc(t.servers, func(s resourceServer) bool { return s.name == args.Server })
	if i < 0 {
		return tool.Errorf("no connected MCP server with resources is called %q", args.Server), nil
	}
	rs := t.servers[i]
	if rs.local && cc.WorkspaceID == "" {
		return tool.Errorf("%s runs in a workspace, and this session has none", rs.name), nil
	}
	c, err := t.pool.connection(ctx, rs.id, workspaceFor(rs, cc), false)
	if err != nil {
		return tool.Errorf("The MCP server %s is not available: %v", rs.name, err), nil
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	res, err := c.client.ReadResource(ctx, args.URI, t.pool.elicitor(cc, rs.name))
	if err != nil {
		return callFailed(rs.name, err), nil
	}
	parts := make([]string, 0, len(res.Contents))
	for i := range res.Contents {
		parts = append(parts, embeddedText(&res.Contents[i]))
	}
	details, _ := json.Marshal(ResultDetails{Server: rs.name, Tool: readResourceTool, Content: ResourceDetails(res.Contents)})
	return tool.Result{Content: truncateMiddle(strings.Join(parts, "\n\n"), maxResultBytes), Details: details}, nil
}

// workspaceFor is the workspace a resource tool reaches a server in.
func workspaceFor(rs resourceServer, cc tool.CallContext) string {
	if rs.local {
		return cc.WorkspaceID
	}
	return ""
}
