package builtin_test

import (
	"strings"
	"testing"
)

func TestEditReplacesUniqueString(t *testing.T) {
	w := newWorkspace(t)
	w.write("a.go", "package a\n\nfunc main() {}\n")

	res := w.call("edit", map[string]any{
		"path":       "a.go",
		"old_string": "func main() {}",
		"new_string": "func main() { println(1) }",
	})
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Content)
	}
	if got := w.read("a.go"); got != "package a\n\nfunc main() { println(1) }\n" {
		t.Errorf("file = %q", got)
	}
	if !strings.Contains(res.Content, "line 3") {
		t.Errorf("content = %q, want the line number", res.Content)
	}
	if string(res.Details) == "" {
		t.Error("details are empty")
	}
}

func TestEditErrors(t *testing.T) {
	w := newWorkspace(t)
	w.write("a.txt", "same\nsame\nother\n")

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			"not found",
			map[string]any{"path": "a.txt", "old_string": "missing", "new_string": "x"},
			"was not found",
		},
		{
			"ambiguous",
			map[string]any{"path": "a.txt", "old_string": "same", "new_string": "x"},
			"appears 2 times",
		},
		{
			"identical strings",
			map[string]any{"path": "a.txt", "old_string": "other", "new_string": "other"},
			"identical",
		},
		{
			"empty old_string",
			map[string]any{"path": "a.txt", "old_string": "", "new_string": "x"},
			"old_string is required",
		},
		{
			"missing file",
			map[string]any{"path": "nope.txt", "old_string": "a", "new_string": "b"},
			"edit nope.txt",
		},
		{
			"traversal",
			map[string]any{"path": "../escape", "old_string": "a", "new_string": "b"},
			"outside the workspace root",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := w.call("edit", c.args)
			if !res.IsError {
				t.Fatalf("edit succeeded: %s", res.Content)
			}
			if !strings.Contains(res.Content, c.want) {
				t.Errorf("content = %q, want it to mention %q", res.Content, c.want)
			}
		})
	}
	if got := w.read("a.txt"); got != "same\nsame\nother\n" {
		t.Errorf("a failed edit changed the file: %q", got)
	}
}
