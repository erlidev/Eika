package eikad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

// Bounds on one /process connection.
const (
	// processStartTimeout bounds how long the daemon waits for the start
	// message after the handshake.
	processStartTimeout = 30 * time.Second
	// processStopGrace is how long a process has to exit after its stdin
	// closes, and again after SIGTERM, before it is killed.
	processStopGrace = 2 * time.Second
	// MaxProcessMessage bounds one message on a /process socket. A stdin
	// message carries one whole MCP message, up to 16 MiB before base64.
	MaxProcessMessage = 24 << 20
)

// handleProcess upgrades to a WebSocket and runs one long-lived process with
// its standard streams on the socket: the first message starts it, stdin
// messages feed it, and the daemon sends its stdout and stderr and, last, how
// it ended. Closing the socket stops the process: its stdin closes, then it
// is sent SIGTERM, then killed, which is how an MCP client ends a stdio
// server.
func (d *Daemon) handleProcess(w http.ResponseWriter, r *http.Request) {
	// The origin check is skipped because the only client is the harness,
	// which authenticates with the bearer token rather than a browser origin.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		d.log.Error("accept process websocket", "error", err)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(MaxProcessMessage)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	start, err := readStart(ctx, conn)
	if err != nil {
		d.endProcess(ctx, conn, ProcessMessage{Type: ProcessError, Error: err.Error()})
		return
	}
	dir, err := d.resolve(start.Dir)
	if err != nil {
		d.endProcess(ctx, conn, ProcessMessage{Type: ProcessError, Error: err.Error()})
		return
	}

	// The process ends with procCtx: SIGTERM to its group first, then, after
	// the grace period, SIGKILL to the leader and the pipes closed.
	procCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	cmd := exec.CommandContext(procCtx, start.Command, start.Args...)
	cmd.Dir = dir
	cmd.Env = append(sanitizedEnv(), start.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = processStopGrace
	cmd.Stdout = processWriter{ctx: ctx, conn: conn, stream: ProcessStdout}
	cmd.Stderr = processWriter{ctx: ctx, conn: conn, stream: ProcessStderr}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		d.endProcess(ctx, conn, ProcessMessage{Type: ProcessError, Error: fmt.Sprintf("open stdin: %v", err)})
		return
	}
	if err := cmd.Start(); err != nil {
		d.endProcess(ctx, conn, ProcessMessage{Type: ProcessError, Error: fmt.Sprintf("start %s: %v", start.Command, err)})
		return
	}

	// The input goroutine owns stdin; the client leaving closes it.
	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		defer stdin.Close()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg ProcessMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				d.log.Warn("decode process message", "error", err)
				continue
			}
			if msg.Type == ProcessStdin {
				if _, err := stdin.Write(msg.Data); err != nil {
					return
				}
			}
		}
	}()

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-exited:
	case <-clientGone:
		// Stdin is closed; a well-behaved server exits on its own.
		select {
		case waitErr = <-exited:
		case <-time.After(processStopGrace):
			stop()
			waitErr = <-exited
		}
	}
	// Whatever the leader left behind in its group goes with it.
	_ = killGroup(cmd)

	code := 0
	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
	case errors.As(waitErr, &exitErr):
		code = exitErr.ExitCode()
	default:
		d.endProcess(ctx, conn, ProcessMessage{Type: ProcessError, Error: fmt.Sprintf("run %s: %v", start.Command, waitErr)})
		return
	}
	d.endProcess(ctx, conn, ProcessMessage{Type: ProcessExit, ExitCode: code})
}

// readStart reads the message that starts the process.
func readStart(ctx context.Context, conn *websocket.Conn) (ProcessMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, processStartTimeout)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return ProcessMessage{}, fmt.Errorf("read start message: %w", err)
	}
	var start ProcessMessage
	if err := json.Unmarshal(data, &start); err != nil {
		return ProcessMessage{}, fmt.Errorf("decode start message: %w", err)
	}
	if start.Type != ProcessStart {
		return ProcessMessage{}, fmt.Errorf("the first message is %q, not %q", start.Type, ProcessStart)
	}
	if strings.TrimSpace(start.Command) == "" {
		return ProcessMessage{}, errors.New("the start message has no command")
	}
	return start, nil
}

// endProcess sends the last message and closes the socket.
func (d *Daemon) endProcess(ctx context.Context, conn *websocket.Conn, msg ProcessMessage) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ptyExitTimeout)
	defer cancel()
	if data, err := json.Marshal(msg); err == nil {
		_ = conn.Write(ctx, websocket.MessageText, data)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// processWriter forwards one of the process's output streams. A client that
// stops reading fails the write, which ends the copying.
type processWriter struct {
	ctx    context.Context
	conn   *websocket.Conn
	stream string
}

func (w processWriter) Write(p []byte) (int, error) {
	data, err := json.Marshal(ProcessMessage{Type: w.stream, Data: p})
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(w.ctx, ptyWriteTimeout)
	defer cancel()
	if err := w.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return 0, err
	}
	return len(p), nil
}
