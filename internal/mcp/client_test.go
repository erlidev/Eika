package mcp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/mcp/mcptest"
)

var eika = mcp.Implementation{Name: "eika", Title: "Eika", Version: "test"}

// echoTool answers with its arguments.
func echoTool(name string) mcptest.Tool {
	return mcptest.Tool{
		Definition: mcptest.ToolDef(name, "Echo the arguments.", `{"type":"object","properties":{"text":{"type":"string"}}}`),
		Call: func(_ context.Context, c *mcptest.Call) (any, *mcptest.Error) {
			return mcptest.Text("echo " + string(c.Arguments)), nil
		},
	}
}

func serve(t *testing.T, s *mcptest.Server) string {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return srv.URL + "/mcp"
}

func connect(t *testing.T, url string, handlers mcp.Handlers) *mcp.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := mcp.ConnectHTTP(ctx, mcp.HTTPTarget{URL: url, Client: http.DefaultClient}, eika, handlers)
	if err != nil {
		t.Fatalf("ConnectHTTP: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestConnectHTTPFindsTheEra(t *testing.T) {
	tests := []struct {
		era       string
		wantEra   mcp.Era
		transport string
		version   string
		methods   []string
	}{
		{mcptest.Modern, mcp.EraModern, mcp.TransportStreamableHTTP, mcp.Version, []string{"server/discover"}},
		{mcptest.Legacy, mcp.EraLegacy, mcp.TransportStreamableHTTP, "2025-11-25", []string{"server/discover", "initialize", "notifications/initialized"}},
		{mcptest.SSE, mcp.EraLegacy, mcp.TransportSSE, "2025-11-25", []string{"initialize", "notifications/initialized"}},
	}
	for _, tt := range tests {
		t.Run(tt.era, func(t *testing.T) {
			s := &mcptest.Server{Era: tt.era, Name: "scripted", Instructions: "Be kind.", Tools: []mcptest.Tool{echoTool("echo")}}
			c := connect(t, serve(t, s), mcp.Handlers{})
			info := c.Info()
			if info.Era != tt.wantEra || info.Transport != tt.transport || info.Version != tt.version {
				t.Errorf("info = %+v, want era %s over %s at %s", info, tt.wantEra, tt.transport, tt.version)
			}
			if info.Server.Name != "scripted" || info.Server.Version != "1.2.3" || info.Instructions != "Be kind." {
				t.Errorf("server = %+v, instructions %q", info.Server, info.Instructions)
			}
			if got := s.Methods(); !slices.Equal(got, tt.methods) {
				t.Errorf("methods = %v, want %v", got, tt.methods)
			}
			res, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`), mcp.CallOptions{})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if len(res.Content) != 1 || res.Content[0].Text != `echo {"text":"hi"}` {
				t.Errorf("result = %+v", res)
			}
		})
	}
}

func TestModernRequestsCarryTheirMetadata(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{{
		Definition: mcptest.ToolDef("天気", "Weather.", `{"type":"object","properties":{"region":{"type":"string","x-mcp-header":"Region"},"days":{"type":"integer","x-mcp-header":"Days"}}}`),
		Call:       func(context.Context, *mcptest.Call) (any, *mcptest.Error) { return mcptest.Text("sunny"), nil },
	}}}
	c := connect(t, serve(t, s), mcp.Handlers{})
	_, err := c.CallTool(context.Background(), "天気", json.RawMessage(`{"region":"us-west1","days":3}`), mcp.CallOptions{
		Headers: map[string]string{"Region": "us-west1", "Days": "3"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var call mcptest.Request
	for _, r := range s.Requests() {
		if r.Method == "tools/call" {
			call = r
		}
	}
	h := call.Headers
	if h.Get("MCP-Protocol-Version") != mcp.Version || h.Get("Mcp-Method") != "tools/call" {
		t.Errorf("headers = %v", h)
	}
	if want := "=?base64?" + base64.StdEncoding.EncodeToString([]byte("天気")) + "?="; h.Get("Mcp-Name") != want {
		t.Errorf("Mcp-Name = %q, want %q", h.Get("Mcp-Name"), want)
	}
	if h.Get("Mcp-Param-Region") != "us-west1" || h.Get("Mcp-Param-Days") != "3" {
		t.Errorf("parameter headers = %v", h)
	}
	var params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"io.modelcontextprotocol/protocolVersion", "io.modelcontextprotocol/clientInfo", "io.modelcontextprotocol/clientCapabilities"} {
		if _, ok := params.Meta[key]; !ok {
			t.Errorf("_meta lacks %s: %s", key, call.Params)
		}
	}
	if !strings.Contains(string(params.Meta["io.modelcontextprotocol/clientCapabilities"]), `"elicitation"`) {
		t.Errorf("capabilities = %s, want elicitation declared", params.Meta["io.modelcontextprotocol/clientCapabilities"])
	}
}

func TestListToolsFollowsPages(t *testing.T) {
	s := &mcptest.Server{PageSize: 2}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		s.Tools = append(s.Tools, echoTool(name))
	}
	c := connect(t, serve(t, s), mcp.Handlers{})
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, "") != "abcde" {
		t.Errorf("tools = %v", names)
	}
}

func TestProgressArrivesOnTheCallsStream(t *testing.T) {
	for _, era := range []string{mcptest.Modern, mcptest.Legacy} {
		t.Run(era, func(t *testing.T) {
			s := &mcptest.Server{Era: era, Stream: true, Tools: []mcptest.Tool{{
				Definition: mcptest.ToolDef("slow", "Slow.", `{"type":"object"}`),
				Call: func(_ context.Context, c *mcptest.Call) (any, *mcptest.Error) {
					c.Progress(1, 2, "half way")
					c.Progress(2, 2, "done")
					return mcptest.Text("finished"), nil
				},
			}}}
			c := connect(t, serve(t, s), mcp.Handlers{})
			var mu sync.Mutex
			var seen []string
			res, err := c.CallTool(context.Background(), "slow", nil, mcp.CallOptions{Progress: func(p mcp.Progress) {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, p.Message)
			}})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(seen, ",") != "half way,done" || res.Content[0].Text != "finished" {
				t.Errorf("progress %v, result %+v", seen, res)
			}
		})
	}
}

// confirmTool asks for a confirmation the way its era asks: a multi
// round-trip result for a modern server, a request of its own for a legacy
// one.
func confirmTool(era string) mcptest.Tool {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	return mcptest.Tool{
		Definition: mcptest.ToolDef("greet", "Greet someone.", `{"type":"object"}`),
		Call: func(_ context.Context, c *mcptest.Call) (any, *mcptest.Error) {
			if era != mcptest.Modern {
				answer, err := c.Elicit(map[string]any{"message": "Who?", "requestedSchema": schema})
				if err != nil {
					return nil, &mcptest.Error{Code: -32603, Message: err.Error()}
				}
				return mcptest.Text("answer " + string(answer)), nil
			}
			if c.InputResponses == nil {
				return map[string]any{
					"resultType":    "input_required",
					"inputRequests": map[string]any{"who": map[string]any{"method": "elicitation/create", "params": map[string]any{"message": "Who?", "requestedSchema": schema}}},
					"requestState":  "state-1",
				}, nil
			}
			if c.RequestState != "state-1" {
				return nil, &mcptest.Error{Code: -32602, Message: "lost the state"}
			}
			return mcptest.Text("answer " + string(c.InputResponses["who"])), nil
		},
	}
}

func TestElicitationReachesTheCallThatAsked(t *testing.T) {
	for _, era := range []string{mcptest.Modern, mcptest.Legacy} {
		t.Run(era, func(t *testing.T) {
			s := &mcptest.Server{Era: era, Tools: []mcptest.Tool{confirmTool(era)}}
			c := connect(t, serve(t, s), mcp.Handlers{})
			var asked mcp.ElicitRequest
			res, err := c.CallTool(context.Background(), "greet", nil, mcp.CallOptions{
				Elicit: func(_ context.Context, req mcp.ElicitRequest) (mcp.ElicitResult, error) {
					asked = req
					return mcp.ElicitResult{Action: mcp.ElicitAccept, Content: json.RawMessage(`{"name":"Ada"}`)}, nil
				},
			})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if asked.Message != "Who?" {
				t.Errorf("elicitation = %+v", asked)
			}
			if got := res.Content[0].Text; !strings.Contains(got, `"action":"accept"`) || !strings.Contains(got, `"name":"Ada"`) {
				t.Errorf("result = %q, want the answer echoed", got)
			}
		})
	}
}

func TestElicitationWithNobodyToAskIsCancelled(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{confirmTool(mcptest.Modern)}}
	c := connect(t, serve(t, s), mcp.Handlers{})
	res, err := c.CallTool(context.Background(), "greet", nil, mcp.CallOptions{})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(res.Content[0].Text, `"action":"cancel"`) {
		t.Errorf("result = %q, want a cancelled elicitation", res.Content[0].Text)
	}
}

func TestLegacySessionExpiry(t *testing.T) {
	s := &mcptest.Server{Era: mcptest.Legacy, Tools: []mcptest.Tool{echoTool("echo")}}
	c := connect(t, serve(t, s), mcp.Handlers{})
	s.ExpireSessions()
	_, err := c.CallTool(context.Background(), "echo", nil, mcp.CallOptions{})
	if !errors.Is(err, mcp.ErrSessionExpired) {
		t.Errorf("CallTool after expiry = %v, want ErrSessionExpired", err)
	}
}

func TestUnauthorizedServerSaysWhy(t *testing.T) {
	s := &mcptest.Server{
		Authorize: func(string) bool { return false },
		Challenge: `Bearer resource_metadata="https://mcp.example/prm", scope="tools:read"`,
	}
	_, err := mcp.ConnectHTTP(context.Background(), mcp.HTTPTarget{URL: serve(t, s), Client: http.DefaultClient}, eika, mcp.Handlers{})
	var auth *mcp.AuthError
	if !errors.As(err, &auth) {
		t.Fatalf("ConnectHTTP = %v, want an AuthError", err)
	}
	if auth.Status != http.StatusUnauthorized || auth.Challenge.ResourceMetadata != "https://mcp.example/prm" || auth.Challenge.Scope != "tools:read" {
		t.Errorf("auth error = %+v", auth)
	}
}

func TestToolErrorsComeBackAsRPCErrors(t *testing.T) {
	c := connect(t, serve(t, &mcptest.Server{}), mcp.Handlers{})
	_, err := c.CallTool(context.Background(), "missing", nil, mcp.CallOptions{})
	var rpc *mcp.RPCError
	if !errors.As(err, &rpc) || !strings.Contains(rpc.Message, "Unknown tool") {
		t.Errorf("CallTool = %v, want the server's error", err)
	}
}

func TestConnectStdioFindsTheEra(t *testing.T) {
	for _, tt := range []struct {
		era  string
		want mcp.Era
	}{{mcptest.Modern, mcp.EraModern}, {mcptest.Legacy, mcp.EraLegacy}} {
		t.Run(tt.era, func(t *testing.T) {
			s := &mcptest.Server{Era: tt.era, Tools: []mcptest.Tool{echoTool("echo"), confirmTool(tt.era)}}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c, err := mcp.ConnectStdio(ctx, s.Pipe(), eika, mcp.Handlers{})
			if err != nil {
				t.Fatalf("ConnectStdio: %v", err)
			}
			defer c.Close()
			if c.Info().Era != tt.want || c.Info().Transport != mcp.TransportStdio {
				t.Errorf("info = %+v", c.Info())
			}
			res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`), mcp.CallOptions{})
			if err != nil || res.Content[0].Text != `echo {"text":"hi"}` {
				t.Fatalf("CallTool = %+v, %v", res, err)
			}
			res, err = c.CallTool(ctx, "greet", nil, mcp.CallOptions{Elicit: func(context.Context, mcp.ElicitRequest) (mcp.ElicitResult, error) {
				return mcp.ElicitResult{Action: mcp.ElicitDecline}, nil
			}})
			if err != nil || !strings.Contains(res.Content[0].Text, `"decline"`) {
				t.Errorf("greet = %+v, %v; want the declined answer", res, err)
			}
		})
	}
}

func TestStdioProcessExitEndsTheClient(t *testing.T) {
	s := &mcptest.Server{Tools: []mcptest.Tool{echoTool("echo")}}
	pipe := s.Pipe()
	closed := make(chan error, 1)
	c, err := mcp.ConnectStdio(context.Background(), pipe, eika, mcp.Handlers{Closed: func(err error) { closed <- err }})
	if err != nil {
		t.Fatalf("ConnectStdio: %v", err)
	}
	defer c.Close()
	_ = pipe.Close()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the client did not notice the process end")
	}
	if done, _ := c.Done(); !done {
		t.Error("Done = false after the process ended")
	}
	if _, err := c.CallTool(context.Background(), "echo", nil, mcp.CallOptions{}); err == nil {
		t.Error("CallTool succeeded on a dead connection")
	}
}

func TestListChangesArrive(t *testing.T) {
	for _, era := range []string{mcptest.Modern, mcptest.Legacy} {
		t.Run(era, func(t *testing.T) {
			s := &mcptest.Server{Era: era, Tools: []mcptest.Tool{echoTool("echo")}}
			changed := make(chan string, 4)
			c := connect(t, serve(t, s), mcp.Handlers{Notify: func(method string, _ json.RawMessage) { changed <- method }})
			if era == mcptest.Modern {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				go func() { _ = c.Listen(ctx) }()
			}
			// The stream opens asynchronously; announce until it is heard.
			deadline := time.After(5 * time.Second)
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case method := <-changed:
					if method != "notifications/tools/list_changed" {
						t.Errorf("notified %s", method)
					}
					return
				case <-tick.C:
					s.ListChanged()
				case <-deadline:
					t.Fatal("no list change arrived")
				}
			}
		})
	}
}

func TestCancellingACallClosesItsStream(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	s := &mcptest.Server{Tools: []mcptest.Tool{{
		Definition: mcptest.ToolDef("hang", "Hang.", `{"type":"object"}`),
		Call: func(ctx context.Context, _ *mcptest.Call) (any, *mcptest.Error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, &mcptest.Error{Code: -32603, Message: "cancelled"}
		},
	}}}
	c := connect(t, serve(t, s), mcp.Handlers{})
	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "hang", nil, mcp.CallOptions{})
		errs <- err
	}()
	<-started
	cancel()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Errorf("CallTool = %v, want context.Canceled", err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Error("the server did not see the request end")
	}
}
