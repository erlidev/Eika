package sandbox_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
