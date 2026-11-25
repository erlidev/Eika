package volume

import (
	"testing"

	"github.com/erlidev/eika/internal/skill"
)

// A name or a path that could leave its directory is refused before anything
// runs: the executor here is nil, so reaching it would panic.
func TestRejectsNamesAndPaths(t *testing.T) {
	v := New(nil)
	for _, name := range []string{"", "..", "../x", "a/b", ".stage", "-x", "A", "a--b", "a b", "a;b", "$(x)"} {
		if err := v.Stage(t.Context(), name, nil); err == nil {
			t.Errorf("Stage(%q) accepted", name)
		}
		if err := v.Replace(t.Context(), name); err == nil {
			t.Errorf("Replace(%q) accepted", name)
		}
		if err := v.Remove(t.Context(), name); err == nil {
			t.Errorf("Remove(%q) accepted", name)
		}
		if _, err := v.Hash(t.Context(), name); err == nil {
			t.Errorf("Hash(%q) accepted", name)
		}
	}
	for _, p := range []string{"", "/etc/passwd", "../x", "a/../../x", "a//b", "./a", "a/", "a\\b", "a/./b", "."} {
		if err := v.Stage(t.Context(), "ok", []skill.File{{Path: p, Data: []byte("x")}}); err == nil {
			t.Errorf("Stage file path %q accepted", p)
		}
	}
}
