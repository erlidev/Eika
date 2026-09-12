package contextfile_test

import (
	"context"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/contextfile"
	"github.com/erlidev/eika/internal/executor/local"
)

// newWorkspace returns an executor over a temporary directory holding files.
func newWorkspace(t *testing.T, files map[string]string) *local.Executor {
	t.Helper()
	e, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	for path, content := range files {
		if err := e.WriteFile(context.Background(), path, []byte(content)); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return e
}

// paths lists the paths of discovered files in order.
func paths(files []contextfile.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func TestDiscover(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		opts  contextfile.Options
		want  []string
	}{
		{
			name:  "no context files",
			files: map[string]string{"main.go": "package main"},
			want:  nil,
		},
		{
			name:  "root agents file",
			files: map[string]string{"AGENTS.md": "root rules"},
			want:  []string{"AGENTS.md"},
		},
		{
			name:  "claude file as a fallback",
			files: map[string]string{"CLAUDE.md": "root rules"},
			want:  []string{"CLAUDE.md"},
		},
		{
			name:  "agents file wins over claude",
			files: map[string]string{"AGENTS.md": "a", "CLAUDE.md": "c"},
			want:  []string{"AGENTS.md"},
		},
		{
			name:  "override replaces the directory's files",
			files: map[string]string{"AGENTS.md": "a", "AGENTS.override.md": "o"},
			want:  []string{"AGENTS.override.md"},
		},
		{
			name: "outermost to innermost",
			files: map[string]string{
				"AGENTS.md":              "root",
				"a/AGENTS.md":            "a",
				"a/b/AGENTS.md":          "b",
				".config/eika/AGENTS.md": "global",
			},
			opts: contextfile.Options{Dir: "a/b"},
			want: []string{".config/eika/AGENTS.md", "AGENTS.md", "a/AGENTS.md", "a/b/AGENTS.md"},
		},
		{
			name: "directories without a file are skipped",
			files: map[string]string{
				"AGENTS.md":     "root",
				"a/b/AGENTS.md": "b",
			},
			opts: contextfile.Options{Dir: "a/b"},
			want: []string{"AGENTS.md", "a/b/AGENTS.md"},
		},
		{
			name:  "empty files are skipped",
			files: map[string]string{"AGENTS.md": "   \n"},
			want:  nil,
		},
		{
			name:  "a custom global path is read",
			files: map[string]string{"shared/rules.md": "global"},
			opts:  contextfile.Options{GlobalPath: "shared/rules.md"},
			want:  []string{"shared/rules.md"},
		},
		{
			name:  "a directory outside the root falls back to the root",
			files: map[string]string{"AGENTS.md": "root"},
			opts:  contextfile.Options{Dir: "../.."},
			want:  []string{"AGENTS.md"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newWorkspace(t, c.files)
			got, err := contextfile.Discover(context.Background(), e, c.opts)
			if err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("paths = %v, want %v", paths(got), c.want)
			}
			for i := range c.want {
				if got[i].Path != c.want[i] {
					t.Fatalf("paths = %v, want %v", paths(got), c.want)
				}
			}
		})
	}
}

func TestDiscoverReadsContent(t *testing.T) {
	e := newWorkspace(t, map[string]string{"AGENTS.md": "  run make check\n\n"})
	files, err := contextfile.Discover(context.Background(), e, contextfile.Options{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(files) != 1 || files[0].Content != "run make check" {
		t.Errorf("files = %+v", files)
	}
}

func TestSection(t *testing.T) {
	if got := contextfile.Section(nil); got != "" {
		t.Errorf("Section(nil) = %q, want the empty string", got)
	}
	section := contextfile.Section([]contextfile.File{
		{Path: "AGENTS.md", Content: "root rules"},
		{Path: "a/AGENTS.md", Content: "inner rules"},
	})
	for _, want := range []string{"# Workspace instructions", "## AGENTS.md", "root rules", "## a/AGENTS.md", "inner rules"} {
		if !strings.Contains(section, want) {
			t.Errorf("section is missing %q:\n%s", want, section)
		}
	}
	if strings.Index(section, "root rules") > strings.Index(section, "inner rules") {
		t.Error("section does not put the more specific file last")
	}
}
