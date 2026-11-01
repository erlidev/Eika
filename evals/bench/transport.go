package bench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Transport is the runner's connection to the server: the seam the tests
// replace. Every command the runner sends goes through Exec as an argument
// list, so a fake can record exactly what would have run.
type Transport interface {
	// Exec runs the command on the server and returns what it wrote to
	// standard output. A command that exits non-zero is an *ExecError.
	Exec(ctx context.Context, argv ...string) (string, error)
	// Forward makes remote, an address as the server reaches it
	// (host:port), reachable at a loopback address of this machine, and
	// returns that address.
	Forward(ctx context.Context, remote string) (string, error)
	// Reconnect opens the connection again after it was lost.
	Reconnect(ctx context.Context) error
	// Close ends the connection and the forwards on it.
	Close() error
}

// ExecError is a command that ran and failed, or did not run because the
// connection failed (ExitCode is -1 then).
type ExecError struct {
	// Argv is the command.
	Argv []string
	// ExitCode is its exit status, or -1 when it did not run to an end.
	ExitCode int
	// Stderr is what it wrote to standard error, trimmed.
	Stderr string
}

// Error states the command, its status and what it said.
func (e *ExecError) Error() string {
	msg := fmt.Sprintf("run %s: exit %d", strings.Join(e.Argv, " "), e.ExitCode)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

// Lost reports whether err says the connection to the server failed rather
// than a command: ssh exits 255 for its own failures.
func Lost(err error) bool {
	var ee *ExecError
	return errors.As(err, &ee) && (ee.ExitCode == 255 || ee.ExitCode == -1)
}

// quote makes one argument safe for the POSIX shell that ssh runs the
// command line with.
func quote(arg string) string {
	if arg != "" && strings.Trim(arg, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// Command joins an argument list into the command line the server's shell
// runs.
func Command(argv ...string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = quote(a)
	}
	return strings.Join(quoted, " ")
}

// SSH is a Transport over one ControlMaster: a single ssh connection to the
// host, opened once and shared by every command and forward of the run. It
// owns a private directory for the control socket.
type SSH struct {
	host string

	mu  sync.Mutex
	dir string
}

// DialSSH opens the ControlMaster to host, a name ssh resolves from the
// user's configuration, and returns the transport. It uses the connection
// settings of that configuration (key, jump host) and no terminal, so a
// password prompt fails the dial instead of hanging it.
func DialSSH(ctx context.Context, host string) (*SSH, error) {
	s := &SSH{host: host}
	if err := s.Reconnect(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// socket is the control path, short enough for a unix socket.
func (s *SSH) socket() string { return filepath.Join(s.dir, "c") }

// Reconnect starts a new master, ending the old one if it is still there.
func (s *SSH) Reconnect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir != "" {
		_ = s.exit()
		_ = os.RemoveAll(s.dir)
	}
	dir, err := os.MkdirTemp("", "eika-bench-")
	if err != nil {
		return fmt.Errorf("make control directory: %w", err)
	}
	s.dir = dir
	args := []string{
		"-M", "-S", s.socket(), "-fN",
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3",
		"-o", "ControlPersist=no", "-o", "ExitOnForwardFailure=yes",
		s.host,
	}
	if out, err := exec.CommandContext(ctx, "ssh", args...).CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		s.dir = ""
		return fmt.Errorf("open the ssh connection to %s: %w: %s", s.host, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Exec runs argv on the server through the master.
func (s *SSH) Exec(ctx context.Context, argv ...string) (string, error) {
	s.mu.Lock()
	sock := ""
	if s.dir != "" {
		sock = s.socket()
	}
	s.mu.Unlock()
	if sock == "" {
		return "", &ExecError{Argv: argv, ExitCode: -1, Stderr: "no connection"}
	}
	cmd := exec.CommandContext(ctx, "ssh", "-S", sock, "-o", "BatchMode=yes", s.host, Command(argv...))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return stdout.String(), &ExecError{Argv: argv, ExitCode: code, Stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.String(), nil
}

// Forward adds a local forward to remote on the master.
func (s *SSH) Forward(ctx context.Context, remote string) (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("pick a local port: %w", err)
	}
	local := l.Addr().String()
	if err := l.Close(); err != nil {
		return "", fmt.Errorf("pick a local port: %w", err)
	}
	s.mu.Lock()
	sock := s.socket()
	s.mu.Unlock()
	out, err := exec.CommandContext(ctx, "ssh", "-S", sock, "-O", "forward", "-L", local+":"+remote, s.host).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("forward %s: %w: %s", remote, err, strings.TrimSpace(string(out)))
	}
	return local, nil
}

// exit asks the master to end; the caller holds mu.
func (s *SSH) exit() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ssh", "-S", s.socket(), "-O", "exit", s.host).Run()
}

// Close ends the master, which closes the forwards with it.
func (s *SSH) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		return nil
	}
	err := s.exit()
	_ = os.RemoveAll(s.dir)
	s.dir = ""
	return err
}
