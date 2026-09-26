package server

import (
	"bytes"
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/server/servertest"
)

// update rewrites the contract file instead of comparing it: `make contract`.
var update = flag.Bool("update", false, "rewrite docs/api/contract.json from the Go wire types")

// contractFile is the API's wire contract, which the frontend's mock harness
// enforces; see docs/api/README.md.
const contractFile = "../../docs/api/contract.json"

// wire is what one route reads and answers, as Go values whose types are the
// shapes: a zero value of the request and response types, nil for none.
type wire struct {
	status    int
	request   any
	raw       bool
	response  any
	websocket bool
}

// searchKeysResponse is the body PUT /api/search/keys/{name} writes as a map.
type searchKeysResponse struct {
	Keys []searchKeyBody `json:"keys"`
}

// routeWire lists every route under /api with its wire types. A route that
// routes.go registers and this table lacks fails TestContractCoversEveryRoute;
// a type this table names wrongly fails the handler tests, which check every
// response they get against the contract.
var routeWire = map[string]wire{
	"GET /api/healthz":       {status: http.StatusOK, response: health{}},
	"GET /api/auth/status":   {status: http.StatusOK, response: authStatusResponse{}},
	"POST /api/auth/setup":   {status: http.StatusCreated, request: passwordRequest{}, response: signInResponse{}},
	"POST /api/auth/login":   {status: http.StatusOK, request: passwordRequest{}, response: signInResponse{}},
	"POST /api/auth/logout":  {status: http.StatusNoContent},
	"PUT /api/auth/password": {status: http.StatusOK, request: changePasswordRequest{}, response: signInResponse{}},

	"GET /api/events":                   {websocket: true},
	"GET /api/workspaces/{id}/terminal": {websocket: true},

	"GET /api/projects":         {status: http.StatusOK, response: projectsResponse{}},
	"POST /api/projects":        {status: http.StatusCreated, request: createProjectRequest{}, response: projectBody{}},
	"GET /api/projects/{id}":    {status: http.StatusOK, response: projectBody{}},
	"PATCH /api/projects/{id}":  {status: http.StatusOK, request: updateProjectRequest{}, response: projectBody{}},
	"DELETE /api/projects/{id}": {status: http.StatusNoContent},

	"GET /api/workspaces":              {status: http.StatusOK, response: workspacesResponse{}},
	"POST /api/workspaces":             {status: http.StatusCreated, request: createWorkspaceRequest{}, response: workspaceBody{}},
	"GET /api/workspaces/{id}":         {status: http.StatusOK, response: workspaceBody{}},
	"DELETE /api/workspaces/{id}":      {status: http.StatusNoContent},
	"POST /api/workspaces/{id}/start":  {status: http.StatusOK, response: workspaceBody{}},
	"POST /api/workspaces/{id}/stop":   {status: http.StatusOK, response: workspaceBody{}},
	"POST /api/workspaces/{id}/merge":  {status: http.StatusOK, request: mergeRequest{}, response: mergeResponse{}},
	"GET /api/workspaces/{id}/diff":    {status: http.StatusOK, response: diffResponse{}},
	"POST /api/workspaces/{id}/commit": {status: http.StatusOK, request: commitRequest{}, response: commitResponse{}},
	"POST /api/workspaces/{id}/push":   {status: http.StatusOK, request: pushRequest{}, response: pushResponse{}},
	"GET /api/workspaces/{id}/files":   {status: http.StatusOK, response: filesResponse{}},
	"GET /api/workspaces/{id}/file":    {status: http.StatusOK, response: fileResponse{}},
	"PUT /api/workspaces/{id}/file":    {status: http.StatusOK, raw: true, response: fileEntry{}},
	"PUT /api/workspaces/{id}/sandbox": {status: http.StatusOK, request: sandboxBody{}, response: workspaceBody{}},
	"GET /api/workspaces/{id}/usage":   {status: http.StatusOK, response: usageResponse{}},

	"POST /api/workspaces/{id}/ports/{port}/preview": {status: http.StatusOK, response: previewResponse{}},

	"GET /api/sessions":              {status: http.StatusOK, response: sessionsResponse{}},
	"POST /api/sessions":             {status: http.StatusCreated, request: createSessionRequest{}, response: sessionBody{}},
	"GET /api/sessions/{id}":         {status: http.StatusOK, response: sessionResponse{}},
	"DELETE /api/sessions/{id}":      {status: http.StatusNoContent},
	"GET /api/sessions/{id}/outline": {status: http.StatusOK, response: outlineResponse{}},
	"GET /api/sessions/{id}/path":    {status: http.StatusOK, response: pathResponse{}},
	"POST /api/sessions/{id}/head":   {status: http.StatusOK, request: setHeadRequest{}, response: sessionBody{}},
	"POST /api/sessions/{id}/fork":   {status: http.StatusCreated, request: forkRequest{}, response: sessionBody{}},
	"PUT /api/sessions/{id}/tools":   {status: http.StatusOK, request: setToolsRequest{}, response: sessionBody{}},
	"GET /api/sessions/{id}/agents":  {status: http.StatusOK, response: agentsResponse{}},

	"GET /api/sessions/{id}/configuration":         {status: http.StatusOK, response: sessionConfigurationResponse{}},
	"PUT /api/sessions/{id}/profile":               {status: http.StatusOK, request: setSessionProfileRequest{}, response: sessionConfigurationResponse{}},
	"PUT /api/sessions/{id}/overrides":             {status: http.StatusOK, request: profileSettingsBody{}, response: sessionConfigurationResponse{}},
	"GET /api/sessions/{id}/context":               {status: http.StatusOK, response: contextResponse{}},
	"GET /api/sessions/{id}/requests":              {status: http.StatusOK, response: requestsResponse{}},
	"GET /api/sessions/{id}/requests/{request_id}": {status: http.StatusOK, response: contextResponse{}},
	"GET /api/profiles":                            {status: http.StatusOK, response: profilesResponse{}},
	"GET /api/profiles/inherited":                  {status: http.StatusOK, response: configurationBody{}},
	"POST /api/profiles":                           {status: http.StatusCreated, request: profileRequest{}, response: profileBody{}},
	"PUT /api/profiles/{id}":                       {status: http.StatusOK, request: profileRequest{}, response: profileBody{}},
	"DELETE /api/profiles/{id}":                    {status: http.StatusNoContent},

	"POST /api/subagents/{id}/abort":   {status: http.StatusOK, response: agentBody{}},
	"POST /api/sessions/{id}/messages": {status: http.StatusAccepted, request: messageRequest{}, response: runBody{}},
	"GET /api/sessions/{id}/run":       {status: http.StatusOK, response: runStateResponse{}},
	"POST /api/runs/{id}/abort":        {status: http.StatusOK, response: runBody{}},
	"POST /api/questions/{id}/answer":  {status: http.StatusNoContent, request: answerRequest{}},
	"GET /api/tools":                   {status: http.StatusOK, response: toolsResponse{}},

	"GET /api/providers":          {status: http.StatusOK, response: providersResponse{}},
	"POST /api/providers":         {status: http.StatusCreated, request: createProviderRequest{}, response: providerBody{}},
	"POST /api/providers/probe":   {status: http.StatusOK, request: probeRequest{}, response: probeResponse{}},
	"PATCH /api/providers/{id}":   {status: http.StatusOK, request: updateProviderRequest{}, response: providerBody{}},
	"DELETE /api/providers/{id}":  {status: http.StatusNoContent},
	"GET /api/models":             {status: http.StatusOK, response: modelsResponse{}},
	"POST /api/models":            {status: http.StatusCreated, request: createModelRequest{}, response: modelBody{}},
	"POST /api/models/test":       {status: http.StatusOK, request: testModelRequest{}, response: testModelResponse{}},
	"PATCH /api/models/{id}":      {status: http.StatusOK, request: updateModelRequest{}, response: modelBody{}},
	"DELETE /api/models/{id}":     {status: http.StatusNoContent},
	"POST /api/search":            {status: http.StatusOK, request: search.Request{}, response: searchOutcomeBody{}},
	"GET /api/search/status":      {status: http.StatusOK, response: searchStatusResponse{}},
	"PUT /api/search/keys/{name}": {status: http.StatusOK, request: searchKeyRequest{}, response: searchKeysResponse{}},
	"GET /api/settings":           {status: http.StatusOK, response: settingsResponse{}},
	"PUT /api/settings":           {status: http.StatusOK, request: map[string]json.RawMessage{}, response: settingsResponse{}},
	"GET /api/system":             {status: http.StatusOK, response: systemResponse{}},

	"GET /api/mcp/servers":                       {status: http.StatusOK, response: mcpServersResponse{}},
	"POST /api/mcp/servers":                      {status: http.StatusCreated, request: createMCPServerRequest{}, response: mcpServerBody{}},
	"GET /api/mcp/servers/{id}":                  {status: http.StatusOK, response: mcpServerDetails{}},
	"PATCH /api/mcp/servers/{id}":                {status: http.StatusOK, request: updateMCPServerRequest{}, response: mcpServerBody{}},
	"DELETE /api/mcp/servers/{id}":               {status: http.StatusNoContent},
	"POST /api/mcp/servers/{id}/connect":         {status: http.StatusOK, request: mcpWorkspaceRequest{}, response: mcpServerDetails{}},
	"POST /api/mcp/servers/{id}/authorize":       {status: http.StatusOK, request: authorizeRequest{}, response: authorizeResponse{}},
	"DELETE /api/mcp/servers/{id}/authorization": {status: http.StatusNoContent},
	"POST /api/mcp/servers/{id}/resources/read":  {status: http.StatusOK, request: readResourceRequest{}, response: readResourceResponse{}},
	"POST /api/mcp/servers/{id}/prompts/get":     {status: http.StatusOK, request: getPromptRequest{}, response: mcp.RenderedPrompt{}},
	"POST /api/mcp/oauth/callback":               {status: http.StatusOK, request: oauthCallbackRequest{}, response: oauthCallbackResponse{}},
	"POST /api/elicitations/{id}/answer":         {status: http.StatusNoContent, request: elicitationAnswerRequest{}},
}

// eventPayloads lists every event type with its payload type.
var eventPayloads = map[string]any{
	event.TypeTurnStart:        event.TurnStart{},
	event.TypeMessageDelta:     event.MessageDelta{},
	event.TypeReasoningDelta:   event.ReasoningDelta{},
	event.TypeMessageReset:     event.MessageReset{},
	event.TypeToolCall:         event.ToolCall{},
	event.TypeToolOutput:       event.ToolOutput{},
	event.TypeToolResult:       event.ToolResult{},
	event.TypeTurnProgress:     event.TurnProgress{},
	event.TypeTurnEnd:          event.TurnEnd{},
	event.TypeRunError:         event.RunError{},
	event.TypeQuestionAsked:    event.QuestionAsked{},
	event.TypeSubagentStarted:  event.SubagentStarted{},
	event.TypeSubagentFinished: event.SubagentFinished{},
	event.TypeWorkspaceState:   event.WorkspaceState{},
	event.TypeMCPServer:        event.MCPServer{},
	event.TypeMCPElicitation:   event.MCPElicitation{},
	event.TypeSessionTitle:     event.SessionTitle{},
	event.TypeSessionMessage:   event.SessionMessage{},
	event.TypeBusDropped:       event.BusDropped{},
}

// eventToolCall is the wire form event.ToolCall's MarshalJSON writes.
type eventToolCall struct {
	RunID              string          `json:"run_id"`
	CallID             string          `json:"call_id"`
	Name               string          `json:"name"`
	Arguments          json.RawMessage `json:"arguments"`
	ArgumentsMalformed bool            `json:"arguments_malformed,omitempty"`
}

// providerToolCall is the wire form provider.ToolCall's MarshalJSON writes.
type providerToolCall struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Arguments          json.RawMessage `json:"arguments"`
	ArgumentsMalformed bool            `json:"arguments_malformed,omitempty"`
}

// standIns give the wire form of each type that encodes itself. A sample of
// each is checked against its stand-in by TestStandInsMatchTheirEncoding.
var standIns = map[reflect.Type]reflect.Type{
	reflect.TypeFor[event.ToolCall]():         reflect.TypeFor[eventToolCall](),
	reflect.TypeFor[provider.ToolCall]():      reflect.TypeFor[providerToolCall](),
	reflect.TypeFor[provider.ToolArguments](): reflect.TypeFor[json.RawMessage](),
}

// contract builds the contract from the tables above.
func contract(t *testing.T) servertest.Contract {
	t.Helper()
	responses := servertest.Reader{Options: servertest.Options{StandIns: standIns}}
	requests := servertest.Reader{Options: servertest.Options{Request: true, StandIns: standIns}}
	of := func(v any, request bool) *servertest.Shape {
		t.Helper()
		if v == nil {
			return nil
		}
		r := &responses
		if request {
			r = &requests
		}
		s, err := r.Of(reflect.TypeOf(v))
		if err != nil {
			t.Fatalf("shape of %T: %v", v, err)
		}
		return s
	}
	public := publicRoutes(t)
	c := servertest.Contract{
		Routes:     map[string]servertest.Route{},
		Error:      of(errorBody{}, false),
		ErrorCodes: constants(t, "json.go", "code"),
		Events:     map[string]*servertest.Shape{},
	}
	for key, w := range routeWire {
		c.Routes[key] = servertest.Route{
			Status:     w.status,
			Request:    of(w.request, true),
			RawRequest: w.raw,
			Response:   of(w.response, false),
			WebSocket:  w.websocket,
			Public:     public[key],
		}
	}
	for typ, payload := range eventPayloads {
		c.Events[typ] = of(payload, false)
	}
	if len(requests.Types) > 0 {
		t.Fatalf("a request type contains itself: %v", requests.Types)
	}
	c.Types = responses.Types
	return c
}

// TestContractFile keeps docs/api/contract.json in step with the Go wire
// types. When it fails, run `make contract`, then change web/src/api and the
// mock harness in web/e2e/harness to match; the frontend's tests say where.
func TestContractFile(t *testing.T) {
	data, err := json.MarshalIndent(contract(t), "", "  ")
	if err != nil {
		t.Fatalf("encode contract: %v", err)
	}
	data = append(data, '\n')
	if *update {
		if err := os.WriteFile(contractFile, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", contractFile, err)
		}
		return
	}
	stored, err := os.ReadFile(contractFile)
	if err != nil {
		t.Fatalf("read %s: %v (run `make contract` to write it)", contractFile, err)
	}
	if !bytes.Equal(stored, data) {
		t.Errorf("%s is out of date with the Go wire types: run `make contract`, then update web/src/api and the mock harness to match", contractFile)
	}
}

// TestContractCoversEveryRoute keeps routeWire in step with routes.go.
func TestContractCoversEveryRoute(t *testing.T) {
	registered := registeredRoutes(t)
	var missing, extra []string
	for key := range registered {
		if _, ok := routeWire[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range routeWire {
		if _, ok := registered[key]; !ok {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("routes.go registers routes routeWire lacks: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("routeWire lists routes routes.go does not register: %v", extra)
	}
}

// TestContractCoversEveryEvent keeps eventPayloads in step with the event
// types internal/event declares.
func TestContractCoversEveryEvent(t *testing.T) {
	declared := constants(t, "../event/event.go", "Type")
	var listed []string
	for typ := range eventPayloads {
		listed = append(listed, typ)
	}
	sort.Strings(listed)
	if !slices.Equal(declared, listed) {
		t.Errorf("event types = %v, eventPayloads lists %v", declared, listed)
	}
}

// TestStandInsMatchTheirEncoding checks each stand-in against what its type
// actually writes, so a changed MarshalJSON cannot leave the contract behind.
func TestStandInsMatchTheirEncoding(t *testing.T) {
	samples := []any{
		event.ToolCall{RunID: "r", CallID: "c", Name: "bash", Arguments: `{"command":"ls"}`},
		event.ToolCall{RunID: "r", CallID: "c", Name: "bash", Arguments: `{"command":`},
		provider.ToolCall{ID: "c", Name: "bash", Arguments: `{"command":"ls"}`},
		provider.ToolCall{ID: "c", Name: "bash", Arguments: `not json`},
	}
	for _, sample := range samples {
		shape, err := servertest.Of(reflect.TypeOf(sample), servertest.Options{StandIns: standIns})
		if err != nil {
			t.Fatalf("shape of %T: %v", sample, err)
		}
		data, err := json.Marshal(sample)
		if err != nil {
			t.Fatalf("encode %T: %v", sample, err)
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			t.Fatalf("decode %s: %v", data, err)
		}
		if problems := shape.Check(v, false, nil); len(problems) > 0 {
			t.Errorf("%T encodes as %s, which its stand-in does not describe: %v", sample, data, problems)
		}
	}
}

// registeredRoutes reads the patterns under /api that routes.go registers.
func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	routes := map[string]bool{}
	for _, call := range handleCalls(t) {
		routes[call.pattern] = true
	}
	return routes
}

// publicRoutes are the routes routes.go registers on the server's own mux
// rather than behind the token check.
func publicRoutes(t *testing.T) map[string]bool {
	t.Helper()
	public := map[string]bool{}
	for _, call := range handleCalls(t) {
		public[call.pattern] = call.onServerMux
	}
	return public
}

type handleCall struct {
	pattern     string
	onServerMux bool
}

// handleCalls finds every `x.HandleFunc("METHOD /api/...", ...)` in
// routes.go, and whether x is the server's own mux, `s.mux`.
func handleCalls(t *testing.T) []handleCall {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "routes.go", nil, 0)
	if err != nil {
		t.Fatalf("parse routes.go: %v", err)
	}
	var calls []handleCall
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("pattern %s: %v", lit.Value, err)
		}
		if _, path, ok := strings.Cut(pattern, " "); !ok || !strings.HasPrefix(path, "/api/") {
			return true
		}
		mux, ok := sel.X.(*ast.SelectorExpr)
		calls = append(calls, handleCall{pattern: pattern, onServerMux: ok && mux.Sel.Name == "mux"})
		return true
	})
	return calls
}

// constants returns the sorted values of the string constants in a file
// whose names start with prefix.
func constants(t *testing.T, path, prefix string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var values []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, prefix) || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("constant %s: %v", name.Name, err)
				}
				values = append(values, value)
			}
		}
	}
	sort.Strings(values)
	return values
}
