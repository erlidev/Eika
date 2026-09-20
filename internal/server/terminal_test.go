package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/executor/sandbox"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/workspace"
)

// sandboxToken is the eikad token of the scripted sandbox.
const sandboxToken = "sandbox-token"

// fakeShell is a scripted eikad /pty: it echoes every message back byte for
// byte, answers "exit" with an exit message and a normal close, and reports
// what it saw.
type fakeShell struct {
	// received carries every message the shell read, as sent.
	received chan string
	// closed carries the status the other side closed the socket with.
	closed chan websocket.StatusCode
	// size carries the rows and cols of each handshake.
	size chan string
}

// newFakeShell serves a scripted /pty and returns it with its base URL.
func newFakeShell(t *testing.T) (*fakeShell, string) {
	t.Helper()
	sh := &fakeShell{
		received: make(chan string, 16),
		closed:   make(chan websocket.StatusCode, 1),
		size:     make(chan string, 1),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pty" || r.Header.Get("Authorization") != "Bearer "+sandboxToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		sh.size <- r.URL.Query().Get("rows") + "x" + r.URL.Query().Get("cols")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				sh.closed <- websocket.CloseStatus(err)
				return
			}
			sh.received <- string(data)
			if strings.Contains(string(data), `"ZXhpdA=="`) {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"exit","exit_code":3}`))
				_ = conn.Close(websocket.StatusNormalClosure, "shell exited")
				return
			}
			if err := conn.Write(r.Context(), typ, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return sh, srv.URL
}

// terminalHost is a workspace host with one workspace, whose terminal is the
// scripted shell. Everything else a host does is out of the proxy's reach.
type terminalHost struct {
	server.Workspaces
	state   workspace.State
	address string
}

// Inspect reports the one workspace, w1.
func (h terminalHost) Inspect(_ context.Context, id string) (workspace.Workspace, error) {
	if id != "w1" {
		return workspace.Workspace{}, fmt.Errorf("%w: %s", workspace.ErrNoWorkspace, id)
	}
	return workspace.Workspace{ID: id, State: h.state, Address: h.address, Token: sandboxToken}, nil
}

// Terminal dials the scripted shell with the real sandbox client.
func (h terminalHost) Terminal(ctx context.Context, ws workspace.Workspace, rows, cols uint16) (*websocket.Conn, error) {
	c, err := sandbox.New(sandbox.Options{BaseURL: ws.Address, Token: ws.Token})
	if err != nil {
		return nil, err
	}
	return c.Terminal(ctx, rows, cols)
}

// terminalServer serves the API over a host whose workspace is in state.
func terminalServer(t *testing.T, state workspace.State) (*fakeShell, string) {
	t.Helper()
	sh, address := newFakeShell(t)
	s := server.New(testConfig(), testLogger(), server.Deps{
		Workspaces: terminalHost{state: state, address: address},
	}, server.Options{})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return sh, srv.URL
}

// dialTerminal opens a terminal the way a browser does, with the token in
// the query.
func dialTerminal(t *testing.T, base, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(base, "http") + "/api/workspaces/w1/terminal?token=" + testToken + query
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial terminal: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// receive waits for the next value on ch.
func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func TestTerminalRelaysFramesUnchangedBothWays(t *testing.T) {
	sh, base := terminalServer(t, workspace.StateRunning)
	conn := dialTerminal(t, base, "&rows=30&cols=120")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if size := receive(t, sh.size, "the handshake"); size != "30x120" {
		t.Errorf("sandbox terminal size = %s, want 30x120", size)
	}

	// Field order and spacing the harness would not produce itself show that
	// a message is passed on, not decoded and encoded again.
	sent := `{"data":"aGk=",  "type":"input"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(sent)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := receive(t, sh.received, "the input"); got != sent {
		t.Errorf("sandbox received %q, want %q", got, sent)
	}
	_, echoed, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(echoed) != sent {
		t.Errorf("browser received %q, want %q", echoed, sent)
	}

	// The shell exiting reaches the browser as the exit message and then the
	// sandbox's own close.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"input","data":"ZXhpdA=="}`)); err != nil {
		t.Fatalf("write exit: %v", err)
	}
	_, last, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read exit: %v", err)
	}
	if string(last) != `{"type":"exit","exit_code":3}` {
		t.Errorf("last message = %q, want the exit message", last)
	}
	_, _, err = conn.Read(ctx)
	if status := websocket.CloseStatus(err); status != websocket.StatusNormalClosure {
		t.Errorf("close status = %v (%v), want a normal closure", status, err)
	}
}

func TestTerminalPassesTheBrowsersCloseToTheSandbox(t *testing.T) {
	sh, base := terminalServer(t, workspace.StateRunning)
	conn := dialTerminal(t, base, "")
	receive(t, sh.size, "the handshake")

	if err := conn.Close(websocket.StatusGoingAway, "tab closed"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if status := receive(t, sh.closed, "the sandbox to see the close"); status != websocket.StatusGoingAway {
		t.Errorf("sandbox saw close status %v, want going away", status)
	}
}

func TestTerminalRefusesWhatItCannotOpen(t *testing.T) {
	cases := []struct {
		name   string
		state  workspace.State
		path   string
		status int
	}{
		{"a stopped workspace", workspace.StateStopped, "/api/workspaces/w1/terminal", http.StatusConflict},
		{"a missing workspace", workspace.StateRunning, "/api/workspaces/w2/terminal", http.StatusNotFound},
		{"a size that is not a number", workspace.StateRunning, "/api/workspaces/w1/terminal?rows=tall", http.StatusBadRequest},
		{"a size out of range", workspace.StateRunning, "/api/workspaces/w1/terminal?cols=70000", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, address := newFakeShell(t)
			s := server.New(testConfig(), testLogger(), server.Deps{
				Workspaces: terminalHost{state: c.state, address: address},
			}, server.Options{})
			rec := requestWith(t, s, http.MethodGet, c.path, nil, "Bearer "+testToken)
			if rec.Code != c.status {
				t.Errorf("status = %d, want %d: %s", rec.Code, c.status, rec.Body.String())
			}
		})
	}
}
