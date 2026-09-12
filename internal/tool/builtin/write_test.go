package builtin_test

import (
	"strings"
	"testing"
)

func TestWriteCreatesParentDirectories(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("write", map[string]any{"path": "a/b/c.txt", "content": "hello"})
	if res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}
	if got := w.read("a/b/c.txt"); got != "hello" {
		t.Errorf("file = %q, want %q", got, "hello")
	}
	if !strings.Contains(res.Content, "5 bytes") {
		t.Errorf("content = %q, want the byte count", res.Content)
	}
}

func TestWriteReplacesExistingFile(t *testing.T) {
	w := newWorkspace(t)
	w.write("a.txt", "old")
	if res := w.call("write", map[string]any{"path": "a.txt", "content": "new"}); res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}
	if got := w.read("a.txt"); got != "new" {
		t.Errorf("file = %q, want %q", got, "new")
	}
}

func TestWriteErrors(t *testing.T) {
	w := newWorkspace(t)
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing path", map[string]any{"content": "x"}, "path is required"},
		{"traversal", map[string]any{"path": "../escape", "content": "x"}, "outside the workspace root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := w.call("write", c.args)
			if !res.IsError || !strings.Contains(res.Content, c.want) {
				t.Errorf("result = %+v, want an error mentioning %q", res, c.want)
			}
		})
	}
}
