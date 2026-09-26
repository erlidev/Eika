package sandbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/sandbox"
)

const testToken = "test-token"

// newClient runs a daemon over a temporary root and returns a client for it
// together with that root.
func newClient(t *testing.T) (*sandbox.Client, string) {
	t.Helper()
	root := t.TempDir()
	d, err := eikad.New(eikad.Options{Root: root, Token: testToken},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)

	c, err := sandbox.New(sandbox.Options{BaseURL: srv.URL, Token: testToken, Root: d.Root()})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c, d.Root()
}

func TestNewRejectsIncompleteOptions(t *testing.T) {
	cases := map[string]sandbox.Options{
		"no base url": {Token: testToken},
		"no token":    {BaseURL: "http://sandbox:7000"},
		"nothing":     {},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := sandbox.New(opts); err == nil {
				t.Error("New accepted incomplete options")
			}
		})
	}
}

func TestRootDefaults(t *testing.T) {
	c, err := sandbox.New(sandbox.Options{BaseURL: "http://sandbox:7000", Token: testToken})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if c.Root() != sandbox.DefaultRoot {
		t.Errorf("Root() = %q, want %q", c.Root(), sandbox.DefaultRoot)
	}
}

func TestExecStreamsIntoTheWriters(t *testing.T) {
	c, _ := newClient(t)
	var stdout, stderr bytes.Buffer
	res, err := c.Exec(t.Context(), executor.ExecSpec{
		Command: "echo out; echo err >&2; exit 2",
		Shell:   true,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 2 || res.TimedOut {
		t.Errorf("result = %+v, want exit code 2", res)
	}
	if strings.TrimSpace(stdout.String()) != "out" {
		t.Errorf("stdout = %q, want out", stdout.String())
	}
	if strings.TrimSpace(stderr.String()) != "err" {
		t.Errorf("stderr = %q, want err", stderr.String())
	}
}

func TestSetEnvironmentReachesLaterCommands(t *testing.T) {
	c, _ := newClient(t)
	if err := c.SetEnvironment(t.Context(), []string{"HTTP_PROXY=http://proxy:3128"}); err != nil {
		t.Fatalf("set environment: %v", err)
	}
	var stdout bytes.Buffer
	if _, err := c.Exec(t.Context(), executor.ExecSpec{Command: `printf %s "$HTTP_PROXY"`, Shell: true, Stdout: &stdout}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if stdout.String() != "http://proxy:3128" {
		t.Errorf("stdout = %q, want the entry the client set", stdout.String())
	}
	if err := c.SetEnvironment(t.Context(), []string{"NOT AN ENTRY"}); err == nil {
		t.Error("the daemon accepted an entry with no equals sign")
	}
}

func TestExecSendsStdinAndReportsTimeouts(t *testing.T) {
	c, _ := newClient(t)

	var stdout bytes.Buffer
	if _, err := c.Exec(t.Context(), executor.ExecSpec{
		Command: "cat",
		Stdin:   strings.NewReader("piped"),
		Stdout:  &stdout,
	}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if stdout.String() != "piped" {
		t.Errorf("stdout = %q, want piped", stdout.String())
	}

	res, err := c.Exec(t.Context(), executor.ExecSpec{
		Command: "sleep 10",
		Shell:   true,
		Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("result = %+v, want timed out", res)
	}
}

func TestExecMarksTruncatedOutput(t *testing.T) {
	c, _ := newClient(t)
	var stdout, stderr bytes.Buffer
	if _, err := c.Exec(t.Context(), executor.ExecSpec{
		Command: "yes eika | head -c 20000000",
		Shell:   true,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(stderr.String(), "truncated") {
		t.Errorf("stderr = %q, want a truncation notice", stderr.String())
	}
}

func TestExecReportsACommandThatCannotStart(t *testing.T) {
	c, _ := newClient(t)
	if _, err := c.Exec(t.Context(), executor.ExecSpec{Command: "definitely-not-a-command"}); err == nil {
		t.Error("Exec accepted a command that cannot start")
	}
}

func TestFileRoundTrip(t *testing.T) {
	c, root := newClient(t)
	ctx := t.Context()

	if err := c.WriteFile(ctx, "deep/hello.txt", []byte("hello")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "deep", "hello.txt")); err != nil {
		t.Fatalf("the file was not created: %v", err)
	}

	data, err := c.ReadFile(ctx, "deep/hello.txt", executor.ReadOpts{})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("read = %q, want hello", data)
	}

	data, err = c.ReadFile(ctx, "deep/hello.txt", executor.ReadOpts{MaxBytes: 3})
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "hel" {
		t.Errorf("bounded read = %q, want hel", data)
	}

	info, err := c.Stat(ctx, "deep/hello.txt")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Name != "hello.txt" || info.Size != 5 || info.IsDir || info.Mode.Perm() == 0 {
		t.Errorf("stat = %+v, want the written file", info)
	}
	if info.ModTime.Location() != time.UTC {
		t.Errorf("mod time %v is not in UTC", info.ModTime)
	}

	entries, err := c.List(ctx, "deep")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != filepath.Join("deep", "hello.txt") {
		t.Errorf("list = %+v, want one entry", entries)
	}
}

func TestMissingFileIsErrNotFound(t *testing.T) {
	c, _ := newClient(t)
	if _, err := c.ReadFile(t.Context(), "absent", executor.ReadOpts{}); !errors.Is(err, sandbox.ErrNotFound) {
		t.Errorf("ReadFile error = %v, want ErrNotFound", err)
	}
	if _, err := c.Stat(t.Context(), "absent"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Errorf("Stat error = %v, want ErrNotFound", err)
	}
	if _, err := c.List(t.Context(), "absent"); !errors.Is(err, sandbox.ErrNotFound) {
		t.Errorf("List error = %v, want ErrNotFound", err)
	}
}

func TestPathsOutsideTheRootAreRefused(t *testing.T) {
	c, _ := newClient(t)
	ctx := t.Context()
	if _, err := c.ReadFile(ctx, "../escape", executor.ReadOpts{}); err == nil {
		t.Error("ReadFile left the workspace")
	}
	if err := c.WriteFile(ctx, "../escape", []byte("x")); err == nil {
		t.Error("WriteFile left the workspace")
	}
}

func TestASymlinkOutOfTheRootIsAPermissionError(t *testing.T) {
	c, root := newClient(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "out")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := c.List(t.Context(), "out"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("List error = %v, want fs.ErrPermission", err)
	}
}

func TestTerminalRunsAShell(t *testing.T) {
	c, _ := newClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	conn, err := c.Terminal(ctx, 30, 100)
	if err != nil {
		t.Fatalf("terminal: %v", err)
	}
	defer conn.CloseNow()

	input, err := json.Marshal(eikad.PTYMessage{Type: eikad.PTYInput, Data: []byte("stty size; exit 4\n")})
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, input); err != nil {
		t.Fatalf("write input: %v", err)
	}
	var output strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v, output so far %q", err, output.String())
		}
		var msg eikad.PTYMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("decode %q: %v", data, err)
		}
		if msg.Type == eikad.PTYOutput {
			output.Write(msg.Data)
			continue
		}
		if msg.Type != eikad.PTYExit || msg.ExitCode != 4 {
			t.Fatalf("final message = %+v, want exit 4", msg)
		}
		break
	}
	// The size the client asked for is the size the shell got.
	if !strings.Contains(output.String(), "30 100") {
		t.Errorf("output = %q, want the terminal size 30 100", output.String())
	}
}

func TestTerminalReportsARefusedHandshake(t *testing.T) {
	root := t.TempDir()
	d, err := eikad.New(eikad.Options{Root: root, Token: testToken},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)

	c, err := sandbox.New(sandbox.Options{BaseURL: srv.URL, Token: "wrong"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	conn, err := c.Terminal(t.Context(), 0, 0)
	if err == nil {
		conn.CloseNow()
		t.Fatal("the daemon opened a terminal for a bad token")
	}
	if !strings.Contains(err.Error(), "/pty") {
		t.Errorf("error = %v, want it to name the route", err)
	}
}

func TestABadTokenIsRefused(t *testing.T) {
	root := t.TempDir()
	d, err := eikad.New(eikad.Options{Root: root, Token: testToken},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)

	c, err := sandbox.New(sandbox.Options{BaseURL: srv.URL, Token: "wrong"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := c.Stat(t.Context(), "."); err == nil {
		t.Error("the daemon accepted a bad token")
	}
}

// lines collects what a process writes to stderr, a line at a time.
type lines struct {
	mu  sync.Mutex
	got []string
}

func (l *lines) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, line)
}

func (l *lines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
}

// readUntil reads a process's stdout until it holds want.
func readUntil(t *testing.T, r io.Reader, want string) string {
	t.Helper()
	var out strings.Builder
	buf := make([]byte, 256)
	for !strings.Contains(out.String(), want) {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			t.Fatalf("read stdout: %v, output so far %q", err, out.String())
		}
	}
	return out.String()
}

func TestProcessCarriesItsStandardStreams(t *testing.T) {
	c, root := newClient(t)
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stderr lines
	script := `echo "in $(basename "$PWD") $GREETING"; printf 'first\nsecond' >&2; echo >&2; while read line; do echo "got $line"; done`
	p, err := c.Process(t.Context(), sandbox.ProcessSpec{
		Command: "sh", Args: []string{"-c", script}, Dir: "sub", Env: []string{"GREETING=hi"},
	}, stderr.add)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	defer p.Close()
	if _, err := p.Write([]byte("one\ntwo\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out := readUntil(t, p, "got two\n"); out != "in sub hi\ngot one\ngot two\n" {
		t.Errorf("stdout = %q", out)
	}
	// Stdout and stderr are separate pipes, so stderr may still be on its
	// way when stdout has arrived.
	deadline := time.Now().Add(5 * time.Second)
	for len(stderr.all()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := stderr.all(); !slices.Equal(got, []string{"first", "second"}) {
		t.Errorf("stderr lines = %q, want each line once, whole", got)
	}
}

func TestProcessOutlivesTheContextThatStartedIt(t *testing.T) {
	c, _ := newClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	p, err := c.Process(ctx, sandbox.ProcessSpec{Command: "cat"}, nil)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	defer p.Close()
	// The pool connects on a context that ends once connecting is done;
	// the server it started keeps running.
	cancel()
	if _, err := p.Write([]byte("still here\n")); err != nil {
		t.Fatalf("write after the context ended: %v", err)
	}
	readUntil(t, p, "still here\n")
}

func TestProcessReadFailsWithHowItEnded(t *testing.T) {
	c, _ := newClient(t)
	cases := map[string]struct {
		spec sandbox.ProcessSpec
		want string
	}{
		"exit status":    {sandbox.ProcessSpec{Command: "sh", Args: []string{"-c", "echo bye; exit 3"}}, "status 3"},
		"missing binary": {sandbox.ProcessSpec{Command: "no-such-command-anywhere"}, "no-such-command-anywhere"},
		"escaping dir":   {sandbox.ProcessSpec{Command: "true", Dir: "../.."}, "outside the workspace"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var stderr lines
			p, err := c.Process(t.Context(), tc.spec, stderr.add)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			defer p.Close()
			_, err = io.ReadAll(p)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("read error = %v, want one saying %q", err, tc.want)
			}
		})
	}
}

func TestProcessFlushesAnUnfinishedStderrLine(t *testing.T) {
	c, _ := newClient(t)
	var stderr lines
	p, err := c.Process(t.Context(), sandbox.ProcessSpec{Command: "sh", Args: []string{"-c", "printf 'no newline' >&2"}}, stderr.add)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	defer p.Close()
	_, _ = io.ReadAll(p)
	if got := stderr.all(); !slices.Equal(got, []string{"no newline"}) {
		t.Errorf("stderr lines = %q", got)
	}
}

func TestClosingAProcessStopsIt(t *testing.T) {
	c, root := newClient(t)
	marker := filepath.Join(root, "stopped")
	// The process ignores its stdin closing and waits for SIGTERM, which it
	// records before exiting.
	script := `trap 'touch stopped; exit 0' TERM; echo ready; while :; do sleep 0.05; done`
	p, err := c.Process(t.Context(), sandbox.ProcessSpec{Command: "sh", Args: []string{"-c", script}}, nil)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	readUntil(t, p, "ready\n")
	closed := make(chan error, 1)
	go func() { closed <- p.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the process was not stopped after Close")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestProcessReportsARefusedHandshake(t *testing.T) {
	root := t.TempDir()
	d, err := eikad.New(eikad.Options{Root: root, Token: testToken},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)
	c, err := sandbox.New(sandbox.Options{BaseURL: srv.URL, Token: "wrong"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	p, err := c.Process(t.Context(), sandbox.ProcessSpec{Command: "cat"}, nil)
	if err == nil {
		p.Close()
		t.Fatal("the daemon started a process for a bad token")
	}
	if !strings.Contains(err.Error(), "/process") {
		t.Errorf("error = %v, want it to name the route", err)
	}
}
