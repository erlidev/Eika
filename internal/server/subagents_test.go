package server

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
)

func TestWorkdirResolvesRelativePathsInTheProjectDirectory(t *testing.T) {
	ex, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ex.Root(), "projects", "kitchen")
	w := workdir{Executor: ex, dir: dir}
	if err := w.WriteFile(t.Context(), "notes.md", []byte("quotes")); err != nil {
		t.Fatal(err)
	}
	if data, err := ex.ReadFile(t.Context(), "projects/kitchen/notes.md", executor.ReadOpts{}); err != nil || string(data) != "quotes" {
		t.Fatalf("relative write landed elsewhere: %q %v", data, err)
	}
	if err := w.WriteFile(t.Context(), ex.Root()+"/top.md", []byte("top")); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Stat(t.Context(), "top.md"); err != nil {
		t.Fatalf("absolute path moved: %v", err)
	}
	if err := w.WriteFile(t.Context(), "sub/file", nil); err != nil {
		t.Fatal(err)
	}
	for spec, want := range map[string]string{"": dir, "sub": filepath.Join(dir, "sub"), ex.Root(): ex.Root()} {
		var out bytes.Buffer
		if _, err := w.Exec(t.Context(), executor.ExecSpec{Command: "pwd", Dir: spec, Stdout: &out}); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out.String()); got != want {
			t.Errorf("Dir %q ran in %q, want %q", spec, got, want)
		}
	}
	if infos, err := w.List(t.Context(), "."); err != nil || len(infos) != 2 {
		t.Fatalf("List = %+v %v", infos, err)
	}
	if _, err := w.ReadFile(t.Context(), "../../../outside", executor.ReadOpts{}); err == nil {
		t.Fatal("read outside the root")
	}
}
