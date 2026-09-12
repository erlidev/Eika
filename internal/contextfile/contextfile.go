package contextfile

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/erlidev/eika/internal/executor"
)

// The names a context file can have, in the order a directory is searched.
// An override file replaces the other two for its own directory.
const (
	// NameAgents is the name Eika and other agent harnesses agree on.
	NameAgents = "AGENTS.md"
	// NameClaude is read when a repository has no AGENTS.md.
	NameClaude = "CLAUDE.md"
	// NameOverride replaces the other files in its own directory.
	NameOverride = "AGENTS.override.md"
)

// DefaultGlobalPath is where a workspace-wide context file lives when a
// project does not ship one. It is a workspace path like every other path
// here, because discovery reads through the executor.
const DefaultGlobalPath = ".config/eika/AGENTS.md"

// maxFileBytes bounds one context file, so that a large file cannot fill the
// model's context window.
const maxFileBytes = 64 * 1024

// File is one discovered context file. Path is workspace-relative.
type File struct {
	Path    string
	Content string
}

// Options steer discovery. The zero value discovers from the workspace root
// with the default global path.
type Options struct {
	// Dir is the workspace-relative directory the agent works in. Discovery
	// walks from the workspace root down to it.
	Dir string
	// GlobalPath is the workspace path of the global context file. Empty
	// means DefaultGlobalPath; a path that does not exist is skipped.
	GlobalPath string
}

// Discover returns the context files that apply to a directory, ordered from
// the most general to the most specific: the global file, then the workspace
// root, then each directory down to Dir. A file that does not exist is
// skipped, not an error.
func Discover(ctx context.Context, exec executor.Executor, opts Options) ([]File, error) {
	globalPath := opts.GlobalPath
	if globalPath == "" {
		globalPath = DefaultGlobalPath
	}

	var files []File
	if f, ok, err := read(ctx, exec, globalPath); err != nil {
		return nil, err
	} else if ok {
		files = append(files, f)
	}

	for _, dir := range directories(opts.Dir) {
		f, ok, err := readDir(ctx, exec, dir)
		if err != nil {
			return nil, err
		}
		if ok && f.Path != globalPath {
			files = append(files, f)
		}
	}
	return files, nil
}

// Section renders the discovered files as a system prompt section. It returns
// an empty string when there is nothing to say.
func Section(files []File) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Workspace instructions\n\n")
	b.WriteString("These files describe how to work in this workspace. Follow them. " +
		"When two files disagree, the one listed later is more specific and wins.\n")
	for _, f := range files {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", f.Path, strings.TrimRight(f.Content, "\n"))
	}
	return b.String()
}

// directories lists the directories to search, from the workspace root down to
// dir.
func directories(dir string) []string {
	clean := path.Clean(strings.TrimPrefix(strings.ReplaceAll(dir, "\\", "/"), "/"))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") {
		return []string{"."}
	}
	parts := strings.Split(clean, "/")
	out := make([]string, 0, len(parts)+1)
	out = append(out, ".")
	for i := range parts {
		out = append(out, path.Join(parts[:i+1]...))
	}
	return out
}

// readDir returns the context file of one directory, preferring an override
// over AGENTS.md and AGENTS.md over CLAUDE.md.
func readDir(ctx context.Context, exec executor.Executor, dir string) (File, bool, error) {
	for _, name := range []string{NameOverride, NameAgents, NameClaude} {
		f, ok, err := read(ctx, exec, path.Join(dir, name))
		if err != nil || ok {
			return f, ok, err
		}
	}
	return File{}, false, nil
}

// read returns one file if it exists and has content. A path that cannot be
// stat'ed is treated as absent, which is the common case; a path that exists
// but cannot be read is an error worth reporting.
func read(ctx context.Context, exec executor.Executor, p string) (File, bool, error) {
	info, err := exec.Stat(ctx, p)
	if err != nil || info.IsDir {
		return File{}, false, nil
	}
	data, err := exec.ReadFile(ctx, p, executor.ReadOpts{MaxBytes: maxFileBytes})
	if err != nil {
		return File{}, false, fmt.Errorf("read context file %s: %w", p, err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return File{}, false, nil
	}
	return File{Path: p, Content: content}, true, nil
}
