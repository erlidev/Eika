package builtin_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

type failedSearchExecutor struct{}

func (failedSearchExecutor) Root() string { return "/workspace" }

func (failedSearchExecutor) Exec(_ context.Context, spec executor.ExecSpec) (executor.ExecResult, error) {
	if spec.Stderr != nil {
		_, _ = spec.Stderr.Write([]byte("invalid regular expression"))
	}
	return executor.ExecResult{ExitCode: 2}, nil
}

func (failedSearchExecutor) ReadFile(context.Context, string, executor.ReadOpts) ([]byte, error) {
	panic("unexpected ReadFile call")
}

func (failedSearchExecutor) WriteFile(context.Context, string, []byte) error {
	panic("unexpected WriteFile call")
}

func (failedSearchExecutor) Stat(context.Context, string) (executor.FileInfo, error) {
	panic("unexpected Stat call")
}

func (failedSearchExecutor) List(context.Context, string) ([]executor.FileInfo, error) {
	panic("unexpected List call")
}

type failedFallbackFindExecutor struct {
	calls int
}

func (e *failedFallbackFindExecutor) Root() string { return "/workspace" }

func (e *failedFallbackFindExecutor) Exec(_ context.Context, spec executor.ExecSpec) (executor.ExecResult, error) {
	e.calls++
	if e.calls == 1 {
		return executor.ExecResult{ExitCode: 127}, nil
	}
	if spec.Stderr != nil {
		_, _ = spec.Stderr.Write([]byte("permission denied"))
	}
	return executor.ExecResult{ExitCode: 1}, nil
}

func (*failedFallbackFindExecutor) ReadFile(context.Context, string, executor.ReadOpts) ([]byte, error) {
	panic("unexpected ReadFile call")
}

func (*failedFallbackFindExecutor) WriteFile(context.Context, string, []byte) error {
	panic("unexpected WriteFile call")
}

func (*failedFallbackFindExecutor) Stat(context.Context, string) (executor.FileInfo, error) {
	panic("unexpected Stat call")
}

func (*failedFallbackFindExecutor) List(context.Context, string) ([]executor.FileInfo, error) {
	panic("unexpected List call")
}

func callWithExecutor(t *testing.T, name string, args map[string]any, exec executor.Executor) tool.Result {
	t.Helper()
	registry, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	tl, ok := registry.Get(name)
	if !ok {
		t.Fatalf("tool %s is not registered", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("encode arguments: %v", err)
	}
	result, err := tl.Call(context.Background(), tool.CallContext{Exec: exec}, raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func TestGrepFindsMatches(t *testing.T) {
	if !hasBinary("grep") && !hasBinary("rg") {
		t.Skip("neither rg nor grep is installed")
	}
	w := newWorkspace(t)
	w.write("a.go", "package a\nfunc Target() {}\n")
	w.write("b.go", "package b\n")

	res := w.call("grep", map[string]any{"pattern": "func Target"})
	if res.IsError {
		t.Fatalf("grep failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.go") || !strings.Contains(res.Content, "2") {
		t.Errorf("content = %q, want the file and line number", res.Content)
	}
	if strings.Contains(res.Content, "b.go") {
		t.Errorf("content = %q, want only matching files", res.Content)
	}
}

func TestGrepIgnoreCaseAndGlob(t *testing.T) {
	if !hasBinary("grep") && !hasBinary("rg") {
		t.Skip("neither rg nor grep is installed")
	}
	w := newWorkspace(t)
	w.write("a.go", "TARGET\n")
	w.write("a.txt", "target\n")

	res := w.call("grep", map[string]any{"pattern": "target", "ignore_case": true, "glob": "*.go"})
	if res.IsError {
		t.Fatalf("grep failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.go") || strings.Contains(res.Content, "a.txt") {
		t.Errorf("content = %q, want only the Go file", res.Content)
	}
}

func TestGrepReportsNoMatches(t *testing.T) {
	if !hasBinary("grep") && !hasBinary("rg") {
		t.Skip("neither rg nor grep is installed")
	}
	w := newWorkspace(t)
	w.write("a.go", "package a\n")
	res := w.call("grep", map[string]any{"pattern": "zzzz"})
	if res.IsError || !strings.Contains(res.Content, "no matches") {
		t.Errorf("result = %+v, want a no-matches note", res)
	}
}

func TestGrepRequiresPattern(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("grep", map[string]any{})
	if !res.IsError || !strings.Contains(res.Content, "pattern is required") {
		t.Errorf("result = %+v", res)
	}
}

func TestGrepReportsCommandFailure(t *testing.T) {
	res := callWithExecutor(t, "grep", map[string]any{"pattern": "["}, failedSearchExecutor{})
	if !res.IsError || !strings.Contains(res.Content, "exit code 2") {
		t.Errorf("result = %+v, want a command failure", res)
	}
}

func TestFindMatchesGlob(t *testing.T) {
	if !hasBinary("find") && !hasBinary("rg") {
		t.Skip("neither rg nor find is installed")
	}
	w := newWorkspace(t)
	w.write("cmd/main.go", "package main\n")
	w.write("README.md", "# hi\n")

	res := w.call("find", map[string]any{"pattern": "**/*.go"})
	if res.IsError {
		t.Fatalf("find failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "main.go") || strings.Contains(res.Content, "README.md") {
		t.Errorf("content = %q, want only Go files", res.Content)
	}
}

func TestFindMatchesBaseName(t *testing.T) {
	if !hasBinary("find") && !hasBinary("rg") {
		t.Skip("neither rg nor find is installed")
	}
	w := newWorkspace(t)
	w.write("deep/nested/Makefile", "all:\n")
	res := w.call("find", map[string]any{"pattern": "Makefile"})
	if res.IsError || !strings.Contains(res.Content, "Makefile") {
		t.Errorf("result = %+v, want the nested Makefile", res)
	}
}

func TestFindReportsNoMatches(t *testing.T) {
	if !hasBinary("find") && !hasBinary("rg") {
		t.Skip("neither rg nor find is installed")
	}
	w := newWorkspace(t)
	w.write("a.txt", "x\n")
	res := w.call("find", map[string]any{"pattern": "*.rs"})
	if res.IsError || !strings.Contains(res.Content, "no files matching") {
		t.Errorf("result = %+v", res)
	}
}

func TestFindRequiresPattern(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("find", map[string]any{})
	if !res.IsError || !strings.Contains(res.Content, "pattern is required") {
		t.Errorf("result = %+v", res)
	}
}

func TestFindReportsCommandFailure(t *testing.T) {
	res := callWithExecutor(t, "find", map[string]any{"pattern": "*.go"}, failedSearchExecutor{})
	if !res.IsError || !strings.Contains(res.Content, "exit code 2") {
		t.Errorf("result = %+v, want a command failure", res)
	}
}

func TestFindFallbackReportsExitOneAsFailure(t *testing.T) {
	exec := &failedFallbackFindExecutor{}
	res := callWithExecutor(t, "find", map[string]any{"pattern": "*.go"}, exec)
	if !res.IsError || !strings.Contains(res.Content, "exit code 1") {
		t.Errorf("result = %+v, want a fallback command failure", res)
	}
	if exec.calls != 2 {
		t.Errorf("Exec calls = %d, want ripgrep and find", exec.calls)
	}
}
