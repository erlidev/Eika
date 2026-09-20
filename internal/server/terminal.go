package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/workspace"
)

// handleTerminal serves a shell in a workspace: it opens the sandbox's
// terminal, accepts the browser's socket, and relays messages between the two
// unchanged until either side closes. The messages are eikad's PTY frames,
// documented in docs/api/eikad.md.
//
// The sandbox is dialled before the browser's handshake is accepted, so a
// workspace that is missing or stopped is an ordinary error response rather
// than a socket that opens and closes at once.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	rows, err := terminalSize(r, "rows")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cols, err := terminalSize(r, "cols")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	host, err := s.deps.Workspaces.Inspect(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if host.State != workspace.StateRunning {
		s.fail(w, r, conflictf("workspace %s is %s, not running", id, host.State))
		return
	}
	shell, err := s.deps.Workspaces.Terminal(r.Context(), host, rows, cols)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = shell.CloseNow() }()
	shell.SetReadLimit(eikad.MaxPTYMessage)

	// The same origin rule as the event stream: a page on another site must
	// not open a shell with a token it tricked the browser into sending.
	browser, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.cfg.AllowedOrigins,
	})
	if err != nil {
		s.log.Warn("accept terminal", "workspace_id", id, "error", err)
		_ = shell.Close(websocket.StatusGoingAway, "the client did not connect")
		return
	}
	browser.SetReadLimit(eikad.MaxPTYMessage)
	defer func() { _ = browser.CloseNow() }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Each direction has its own goroutine; whichever ends first closes the
	// other side with the status it ended on, and that ends the other one.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		relayClose(shell, relay(ctx, browser, shell))
	}()
	relayClose(browser, relay(ctx, shell, browser))
	cancel()
	wg.Wait()
	s.log.Info("terminal closed", "workspace_id", id)
}

// relay copies messages from one socket to the other until reading or
// writing fails, and returns that failure.
func relay(ctx context.Context, from, to *websocket.Conn) error {
	for {
		typ, data, err := from.Read(ctx)
		if err != nil {
			return err
		}
		writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
		err = to.Write(writeCtx, typ, data)
		cancel()
		if err != nil {
			return err
		}
	}
}

// relayClose closes conn after the relay into or out of it ended with err.
// A close frame from the other side is passed on with its status and reason,
// so that the browser learns how the shell ended and the sandbox learns the
// browser went away. Anything else, a dropped connection, is the harness's
// failure, reported the way closeWith reports one.
func relayClose(conn *websocket.Conn, err error) {
	var closed websocket.CloseError
	if errors.As(err, &closed) && sendable(closed.Code) {
		_ = conn.Close(closed.Code, closed.Reason)
		return
	}
	_ = conn.Close(websocket.StatusInternalError, "terminal connection lost")
}

// sendable reports whether a close status may appear in a close frame. The
// protocol reserves some for reporting locally and forbids sending them.
func sendable(code websocket.StatusCode) bool {
	switch code {
	case websocket.StatusNoStatusRcvd, websocket.StatusAbnormalClosure, websocket.StatusTLSHandshake:
		return false
	default:
		return code >= websocket.StatusNormalClosure
	}
}

// terminalSize reads a terminal dimension from the query. Absent means the
// sandbox's default.
func terminalSize(r *http.Request, name string) (uint16, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return 0, invalidf("%s must be a number from 0 to 65535", name)
	}
	return uint16(n), nil
}
