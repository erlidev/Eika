package executor_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/erlidev/eika/internal/executor"
)

func TestResolve(t *testing.T) {
	root := filepath.FromSlash("/home/eika")
	cases := []struct {
		name string
		path string
		want string
	}{
		{"relative file", "main.go", filepath.FromSlash("/home/eika/main.go")},
		{"nested file", "a/b/c.go", filepath.FromSlash("/home/eika/a/b/c.go")},
		{"dot", ".", root},
		{"cleaned traversal that stays inside", "a/../b", filepath.FromSlash("/home/eika/b")},
		{"absolute path inside the root", "/home/eika/a", filepath.FromSlash("/home/eika/a")},
		{"dotted name that only starts like a parent", "..a", filepath.FromSlash("/home/eika/..a")},
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
	root := filepath.FromSlash("/home/eika")
	// /home holds more than the root, as / did not: a sibling directory and
	// one that only shares the root's name as a prefix must both be refused.
	paths := []string{
		"..", "../etc/passwd", "a/../../etc/passwd", "/etc/passwd",
		"/home", "/home/other", "/home/other/a", "/home/eika/../other", "../other",
		"/home/eikaother", "/home/eikaother/a", "/home/eika-x/a",
	}
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
	root := filepath.FromSlash("/home/eika")
	got, err := executor.Rel(root, filepath.FromSlash("/home/eika/a/b.go"))
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if got != "a/b.go" {
		t.Errorf("Rel = %q, want %q", got, "a/b.go")
	}
	for _, outside := range []string{"/etc", "/home", "/home/other/a", "/home/eikaother"} {
		if _, err := executor.Rel(root, filepath.FromSlash(outside)); !errors.Is(err, executor.ErrPathOutsideRoot) {
			t.Errorf("Rel(%q) error = %v, want ErrPathOutsideRoot", outside, err)
		}
	}
}
