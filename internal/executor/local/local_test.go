package local_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
)

// newExecutor returns an executor rooted at a fresh temporary directory.
func newExecutor(t *testing.T) *local.Executor {
	t.Helper()
	e, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestNewRejectsMissingDirectory(t *testing.T) {
	if _, err := local.New(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("New on a missing directory returned no error")
	}
}

func TestNewRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := local.New(path); err == nil {
		t.Fatal("New on a file returned no error")
	}
}

func TestWriteAndReadFile(t *testing.T) {
	e := newExecutor(t)
	ctx := context.Background()
	if err := e.WriteFile(ctx, "a/b/c.txt", []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := e.ReadFile(ctx, "a/b/c.txt", executor.ReadOpts{})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("ReadFile = %q, want %q", got, "hello")
	}
}

func TestReadFileMaxBytes(t *testing.T) {
	e := newExecutor(t)
	ctx := context.Background()
	if err := e.WriteFile(ctx, "f.txt", []byte("0123456789")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := e.ReadFile(ctx, "f.txt", executor.ReadOpts{MaxBytes: 4})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "0123" {
		t.Errorf("ReadFile = %q, want %q", got, "0123")
	}
}

func TestFileOperationsRejectTraversal(t *testing.T) {
	e := newExecutor(t)
	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
	}{
		{"read", func() error { _, err := e.ReadFile(ctx, "../escape", executor.ReadOpts{}); return err }},
		{"write", func() error { return e.WriteFile(ctx, "../escape", []byte("x")) }},
		{"stat", func() error { _, err := e.Stat(ctx, "../escape"); return err }},
		{"list", func() error { _, err := e.List(ctx, "../"); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.call(); !errors.Is(err, executor.ErrPathOutsideRoot) {
				t.Errorf("error = %v, want ErrPathOutsideRoot", err)
			}
		})
	}
}

func TestResolveRejectsSymlinkOutOfRoot(t *testing.T) {
	e := newExecutor(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(e.Root(), "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := e.ReadFile(context.Background(), "link/secret", executor.ReadOpts{}); !errors.Is(err, executor.ErrPathOutsideRoot) {
		t.Errorf("error = %v, want ErrPathOutsideRoot", err)
	}
}

func TestStatAndList(t *testing.T) {
	e := newExecutor(t)
	ctx := context.Background()
	if err := e.WriteFile(ctx, "dir/file.txt", []byte("abc")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := e.Stat(ctx, "dir/file.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Name != "file.txt" || info.Path != "dir/file.txt" || info.Size != 3 || info.IsDir {
		t.Errorf("Stat = %+v", info)
	}
	entries, err := e.List(ctx, ".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "dir" || !entries[0].IsDir || entries[0].Path != "dir" {
		t.Errorf("List = %+v", entries)
	}
}

func TestExecStreamsOutput(t *testing.T) {
	e := newExecutor(t)
	var out, errOut bytes.Buffer
	res, err := e.Exec(context.Background(), executor.ExecSpec{
		Command: "echo out; echo err 1>&2",
		Shell:   true,
		Stdout:  &out,
		Stderr:  &errOut,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 || res.TimedOut {
		t.Errorf("result = %+v, want exit 0", res)
	}
	if strings.TrimSpace(out.String()) != "out" {
		t.Errorf("stdout = %q", out.String())
	}
	if strings.TrimSpace(errOut.String()) != "err" {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestExecReportsExitCode(t *testing.T) {
	e := newExecutor(t)
	res, err := e.Exec(context.Background(), executor.ExecSpec{Command: "exit 3", Shell: true})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", res.ExitCode)
	}
}

func TestExecRunsInRootAndUsesEnv(t *testing.T) {
	e := newExecutor(t)
	ctx := context.Background()
	if err := e.WriteFile(ctx, "sub/marker", nil); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var out bytes.Buffer
	if _, err := e.Exec(ctx, executor.ExecSpec{
		Command: "ls; echo $EIKA_TEST",
		Shell:   true,
		Dir:     "sub",
		Env:     []string{"EIKA_TEST=set"},
		Stdout:  &out,
	}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(out.String(), "marker") || !strings.Contains(out.String(), "set") {
		t.Errorf("output = %q", out.String())
	}
}

func TestExecTimesOut(t *testing.T) {
	e := newExecutor(t)
	res, err := e.Exec(context.Background(), executor.ExecSpec{
		Command: "sleep 5",
		Shell:   true,
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("result = %+v, want TimedOut", res)
	}
}

func TestExecRejectsDirOutsideRoot(t *testing.T) {
	e := newExecutor(t)
	if _, err := e.Exec(context.Background(), executor.ExecSpec{Command: "true", Dir: ".."}); !errors.Is(err, executor.ErrPathOutsideRoot) {
		t.Errorf("error = %v, want ErrPathOutsideRoot", err)
	}
}

func TestExecReportsMissingCommand(t *testing.T) {
	e := newExecutor(t)
	if _, err := e.Exec(context.Background(), executor.ExecSpec{Command: "eika-does-not-exist"}); err == nil {
		t.Fatal("Exec of a missing command returned no error")
	}
}
