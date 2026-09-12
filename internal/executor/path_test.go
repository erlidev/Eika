package executor_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/erlidev/eika/internal/executor"
)

func TestResolve(t *testing.T) {
	root := filepath.FromSlash("/workspace")
	cases := []struct {
		name string
		path string
		want string
	}{
		{"relative file", "main.go", filepath.FromSlash("/workspace/main.go")},
		{"nested file", "a/b/c.go", filepath.FromSlash("/workspace/a/b/c.go")},
		{"dot", ".", root},
		{"cleaned traversal that stays inside", "a/../b", filepath.FromSlash("/workspace/b")},
		{"absolute path inside the root", "/workspace/a", filepath.FromSlash("/workspace/a")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := executor.Resolve(root, c.path)
			if err != nil {
				t.Fatalf("Resolve(%q) = %v", c.path, err)
			}
			if got != c.want {
				t.Errorf("Resolve(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}

func TestResolveRejectsEscapes(t *testing.T) {
	root := filepath.FromSlash("/workspace")
	paths := []string{"..", "../etc/passwd", "a/../../etc/passwd", "/etc/passwd", "/workspacex/a"}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			if _, err := executor.Resolve(root, p); !errors.Is(err, executor.ErrPathOutsideRoot) {
				t.Errorf("Resolve(%q) error = %v, want ErrPathOutsideRoot", p, err)
			}
		})
	}
}

func TestResolveRejectsEmptyRoot(t *testing.T) {
	if _, err := executor.Resolve("", "a"); err == nil {
		t.Fatal("Resolve with an empty root returned no error")
	}
}

func TestRel(t *testing.T) {
	root := filepath.FromSlash("/workspace")
	got, err := executor.Rel(root, filepath.FromSlash("/workspace/a/b.go"))
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if got != "a/b.go" {
		t.Errorf("Rel = %q, want %q", got, "a/b.go")
	}
	if _, err := executor.Rel(root, filepath.FromSlash("/etc")); !errors.Is(err, executor.ErrPathOutsideRoot) {
		t.Errorf("Rel outside root error = %v, want ErrPathOutsideRoot", err)
	}
}
