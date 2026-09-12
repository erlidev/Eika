package builtin_test

import (
	"strings"
	"testing"
)

func TestRead(t *testing.T) {
	w := newWorkspace(t)
	w.write("a.txt", "one\ntwo\nthree\n")

	res := w.call("read", map[string]any{"path": "a.txt"})
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	want := "     1\tone\n     2\ttwo\n     3\tthree\n"
	if res.Content != want {
		t.Errorf("content = %q, want %q", res.Content, want)
	}
}

func TestReadOffsetAndLimit(t *testing.T) {
	w := newWorkspace(t)
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString("line\n")
	}
	w.write("a.txt", b.String())

	res := w.call("read", map[string]any{"path": "a.txt", "offset": 3, "limit": 2})
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "     3\tline") || !strings.Contains(res.Content, "     4\tline") {
		t.Errorf("content = %q, want lines 3 and 4", res.Content)
	}
	if strings.Contains(res.Content, "     5\t") {
		t.Errorf("content = %q, want the limit respected", res.Content)
	}
	if !strings.Contains(res.Content, "6 more lines") {
		t.Errorf("content = %q, want a note about the remaining lines", res.Content)
	}
}

func TestReadErrors(t *testing.T) {
	w := newWorkspace(t)
	w.write("a.txt", "x\n")
	w.write("bin", "abc\x00def")
	w.write("dir/inner.txt", "x\n")

	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing path", map[string]any{}, "path is required"},
		{"missing file", map[string]any{"path": "nope.txt"}, "read nope.txt"},
		{"directory", map[string]any{"path": "dir"}, "use ls"},
		{"binary", map[string]any{"path": "bin"}, "binary"},
		{"offset past the end", map[string]any{"path": "a.txt", "offset": 99}, "past the last line"},
		{"traversal", map[string]any{"path": "../escape"}, "outside the workspace root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := w.call("read", c.args)
			if !res.IsError {
				t.Fatalf("read succeeded: %s", res.Content)
			}
			if !strings.Contains(res.Content, c.want) {
				t.Errorf("content = %q, want it to mention %q", res.Content, c.want)
			}
		})
	}
}

func TestReadEmptyFile(t *testing.T) {
	w := newWorkspace(t)
	w.write("empty.txt", "")
	res := w.call("read", map[string]any{"path": "empty.txt"})
	if res.IsError || !strings.Contains(res.Content, "is empty") {
		t.Errorf("result = %+v, want an empty-file note", res)
	}
}

func TestReadClipsVeryLongLines(t *testing.T) {
	w := newWorkspace(t)
	w.write("min.js", strings.Repeat("x", 5000)+"\n")
	res := w.call("read", map[string]any{"path": "min.js"})
	if res.IsError {
		t.Fatalf("read failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "more characters") {
		t.Errorf("content does not report the clipped line: %q", res.Content[:80])
	}
	if len(res.Content) > 4000 {
		t.Errorf("content length = %d, want the line clipped", len(res.Content))
	}
}
