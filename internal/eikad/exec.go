package eikad

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// TokenEnv names the environment variable the harness uses to pass the
// daemon's bearer token into a sandbox. The daemon removes it from the
// environment of every command it runs, so an agent never sees it.
const TokenEnv = "EIKAD_TOKEN"

// Bounds on one command.
const (
	// waitDelay is how long a killed command has to release the output pipes
	// before os/exec closes them and lets Wait return.
	waitDelay = 2 * time.Second
	// maxExecOutput caps how much output one command may stream. Past it the
	// daemon stops forwarding and reports the run as truncated.
	maxExecOutput = 8 << 20
)

// killGroup kills a command's whole process group.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// handleExec runs a command and streams its output as newline-delimited JSON
// frames, ending with one frame carrying the exit code.
func (d *Daemon) handleExec(w http.ResponseWriter, r *http.Request) {
	var req ExecRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWriteBytes)).Decode(&req); err != nil {
		writeError(w, d.log, http.StatusBadRequest, fmt.Errorf("decode exec request: %w", err))
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeError(w, d.log, http.StatusBadRequest, errors.New("exec request has no command"))
		return
	}
	dir, err := d.resolve(req.Dir)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}

	ctx := r.Context()
	if req.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	name, args := req.Command, req.Args
	if req.Shell {
		name, args = "sh", []string{"-c", req.Command}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(d.environ(), req.Env...)
	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}
	// The command leads its own process group, and cancellation kills the
	// whole group: a backgrounded grandchild that still holds the output pipes
	// would otherwise keep Wait blocked long after the timeout fired.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = waitDelay

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	frames := newFrameStream(w)
	cmd.Stdout = frames.writer("stdout")
	cmd.Stderr = frames.writer("stderr")

	if err := cmd.Start(); err != nil {
		frames.send(ExecFrame{Error: fmt.Sprintf("start %s: %v", name, err)})
		return
	}
	waitErr := cmd.Wait()

	// A deadline that fired while the client is still connected is a timeout;
	// a cancelled request is the client giving up.
	timedOut := req.TimeoutMS > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) && r.Context().Err() == nil
	code := 0
	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
	case errors.As(waitErr, &exitErr):
		code = exitErr.ExitCode()
	default:
		frames.send(ExecFrame{Error: fmt.Sprintf("run %s: %v", name, waitErr)})
		return
	}
	frames.send(ExecFrame{ExitCode: &code, TimedOut: timedOut, Truncated: frames.truncated()})
}

// frameStream serialises the frames of one /exec response. stdout and stderr
// are written by two goroutines inside os/exec, so the encoder is guarded.
type frameStream struct {
	// mu guards everything below it.
	mu      sync.Mutex
	enc     *json.Encoder
	ctrl    *http.ResponseController
	written int64
	cut     bool
}

// newFrameStream writes frames to w, flushing each one so that the harness
// sees output while the command is still running.
func newFrameStream(w http.ResponseWriter) *frameStream {
	return &frameStream{enc: json.NewEncoder(w), ctrl: http.NewResponseController(w)}
}

// send writes one frame. A write failure means the client is gone, which the
// command's own context cancellation already handles.
func (s *frameStream) send(f ExecFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.enc.Encode(f); err != nil {
		return
	}
	_ = s.ctrl.Flush()
}

// sendOutput writes one output frame unless the command has already produced
// more than maxExecOutput bytes, in which case the output is dropped and the
// run is reported as truncated.
func (s *frameStream) sendOutput(stream string, data []byte) {
	s.mu.Lock()
	if s.written >= maxExecOutput {
		s.cut = true
		s.mu.Unlock()
		return
	}
	if room := maxExecOutput - s.written; int64(len(data)) > room {
		data, s.cut = data[:room], true
	}
	s.written += int64(len(data))
	s.mu.Unlock()
	s.send(ExecFrame{Stream: stream, Data: data})
}

// truncated reports whether output was dropped.
func (s *frameStream) truncated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cut
}

// writer returns an io.Writer that turns each write into one output frame.
func (s *frameStream) writer(stream string) io.Writer {
	return streamWriter{stream: stream, frames: s}
}

// streamWriter sends what is written to it as frames of one output stream.
type streamWriter struct {
	stream string
	frames *frameStream
}

// Write emits p as a single frame.
func (w streamWriter) Write(p []byte) (int, error) {
	w.frames.sendOutput(w.stream, bytes.Clone(p))
	return len(p), nil
}
