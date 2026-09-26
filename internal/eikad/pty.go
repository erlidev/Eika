package eikad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// Terminal defaults used when the client does not send a size first.
const (
	defaultRows = 24
	defaultCols = 80
	// ptyExitTimeout bounds how long the daemon waits to deliver the final
	// exit message to a client that has stopped reading.
	ptyExitTimeout = 5 * time.Second
	// ptyWriteTimeout bounds one output message, so that a client that has
	// stopped reading cannot keep the terminal alive.
	ptyWriteTimeout = 30 * time.Second
	// MaxPTYMessage bounds one message on a /pty socket in either direction.
	// A pasted block of text arrives as one input message, so the bound is
	// well above the WebSocket library's 32 KiB default.
	MaxPTYMessage = 1 << 20
)

// handlePTY upgrades to a WebSocket and runs an interactive shell on a
// pseudo-terminal. The client sends input and resize messages and receives
// output messages followed by one exit message.
func (d *Daemon) handlePTY(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dir, err := d.resolve(q.Get("dir"))
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}

	// The origin check is skipped because the only client is the harness,
	// which authenticates with the bearer token rather than a browser origin.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		d.log.Error("accept pty websocket", "error", err)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(MaxPTYMessage)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	cmd := exec.CommandContext(ctx, shellPath(q.Get("shell")))
	cmd.Dir = dir
	cmd.Env = append(d.environ(), "TERM=xterm-256color")
	// The shell leads the session's process group; cancelling the request
	// kills the group, so a lingering child cannot keep Wait blocked.
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = waitDelay
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: size(q.Get("rows"), defaultRows),
		Cols: size(q.Get("cols"), defaultCols),
	})
	if err != nil {
		conn.Close(websocket.StatusInternalError, fmt.Sprintf("start shell: %v", err))
		return
	}
	defer tty.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		d.pumpTerminalInput(ctx, conn, tty)
	}()

	d.pumpTerminalOutput(ctx, conn, tty)

	code := 0
	var exitErr *exec.ExitError
	if err := cmd.Wait(); errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	}
	exitCtx, exitCancel := context.WithTimeout(context.WithoutCancel(ctx), ptyExitTimeout)
	defer exitCancel()
	sendPTY(exitCtx, conn, PTYMessage{Type: PTYExit, ExitCode: code})
	_ = conn.Close(websocket.StatusNormalClosure, "")

	cancel()
	wg.Wait()
}

// pumpTerminalInput applies the client's input and resize messages to the
// terminal until the connection ends.
func (d *Daemon) pumpTerminalInput(ctx context.Context, conn *websocket.Conn, tty *os.File) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg PTYMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			d.log.Warn("decode pty message", "error", err)
			continue
		}
		switch msg.Type {
		case PTYInput:
			if _, err := tty.Write(msg.Data); err != nil {
				return
			}
		case PTYResize:
			if err := pty.Setsize(tty, &pty.Winsize{Rows: msg.Rows, Cols: msg.Cols}); err != nil {
				d.log.Warn("resize pty", "error", err)
			}
		}
	}
}

// pumpTerminalOutput forwards terminal output to the client until the shell
// exits or the client goes away.
func (d *Daemon) pumpTerminalOutput(ctx context.Context, conn *websocket.Conn, tty *os.File) {
	buf := make([]byte, 32*1024)
	for {
		n, err := tty.Read(buf)
		// Each write gets its own deadline: a client that stops reading must
		// not keep the shell and this goroutine alive indefinitely.
		if n > 0 && !d.writeTerminalOutput(ctx, conn, buf[:n]) {
			return
		}
		if err != nil {
			return
		}
	}
}

// writeTerminalOutput sends one output message under its own deadline.
func (d *Daemon) writeTerminalOutput(ctx context.Context, conn *websocket.Conn, data []byte) bool {
	writeCtx, cancel := context.WithTimeout(ctx, ptyWriteTimeout)
	defer cancel()
	return sendPTY(writeCtx, conn, PTYMessage{Type: PTYOutput, Data: data})
}

// sendPTY writes one message and reports whether the connection is still
// usable.
func sendPTY(ctx context.Context, conn *websocket.Conn, msg PTYMessage) bool {
	data, err := json.Marshal(msg)
	if err != nil {
		return false
	}
	return conn.Write(ctx, websocket.MessageText, data) == nil
}

// shellPath picks the shell to run: the requested one, then $SHELL, then the
// shells every sandbox image is expected to have.
func shellPath(requested string) string {
	candidates := []string{requested, os.Getenv("SHELL"), "/bin/bash", "/bin/sh"}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}
	return "/bin/sh"
}

// size parses a terminal dimension, falling back to a default.
func size(raw string, fallback uint16) uint16 {
	n, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || n == 0 {
		return fallback
	}
	return uint16(n)
}
