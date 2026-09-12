package builtin_test

import (
	"strings"
	"testing"
)

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
