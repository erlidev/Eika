package builtin_test

import (
	"strings"
	"testing"
)

func TestLsListsDirectoriesFirst(t *testing.T) {
	w := newWorkspace(t)
	w.write("z.txt", "abc")
	w.write("sub/inner.txt", "x")

	res := w.call("ls", map[string]any{})
	if res.IsError {
		t.Fatalf("ls failed: %s", res.Content)
	}
	want := "sub/\nz.txt (3 bytes)"
	if res.Content != want {
		t.Errorf("content = %q, want %q", res.Content, want)
	}
}

func TestLsSubdirectory(t *testing.T) {
	w := newWorkspace(t)
	w.write("sub/inner.txt", "x")
	res := w.call("ls", map[string]any{"path": "sub"})
	if res.IsError || !strings.Contains(res.Content, "inner.txt") {
		t.Errorf("result = %+v", res)
	}
}

func TestLsLimit(t *testing.T) {
	w := newWorkspace(t)
	for _, name := range []string{"a", "b", "c"} {
		w.write(name, "x")
	}
	res := w.call("ls", map[string]any{"limit": 2})
	if res.IsError {
		t.Fatalf("ls failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "1 more entries") {
		t.Errorf("content = %q, want a note about the remaining entries", res.Content)
	}
}

func TestLsErrors(t *testing.T) {
	w := newWorkspace(t)
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing directory", map[string]any{"path": "nope"}, "ls nope"},
		{"traversal", map[string]any{"path": ".."}, "outside the workspace root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := w.call("ls", c.args)
			if !res.IsError || !strings.Contains(res.Content, c.want) {
				t.Errorf("result = %+v, want an error mentioning %q", res, c.want)
			}
		})
	}
}

func TestLsEmptyDirectory(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("ls", map[string]any{})
	if res.IsError || !strings.Contains(res.Content, "is empty") {
		t.Errorf("result = %+v", res)
	}
}
