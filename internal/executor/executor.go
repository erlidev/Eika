package executor

import (
	"context"
	"io"
	"io/fs"
	"time"
)

// ErrNotFound reports that a workspace path does not exist. Executor
// implementations must wrap this error for missing files and directories.
var ErrNotFound = fs.ErrNotExist

// Executor runs commands and file operations inside one workspace root.
type Executor interface {
	// Root reports the absolute path of the workspace root inside the
	// execution environment.
	Root() string
	// Exec runs a command, streaming its output to the writers in spec.
	Exec(ctx context.Context, spec ExecSpec) (ExecResult, error)
	// ReadFile returns the contents of a workspace-relative path.
	ReadFile(ctx context.Context, path string, opts ReadOpts) ([]byte, error)
	// WriteFile writes data to a workspace-relative path, creating parent
	// directories as needed.
	WriteFile(ctx context.Context, path string, data []byte) error
	// Stat describes one workspace-relative path.
	Stat(ctx context.Context, path string) (FileInfo, error)
	// List describes the direct children of a workspace-relative directory.
	List(ctx context.Context, path string) ([]FileInfo, error)
}

// ExecSpec describes one command run. If Shell is true, Command is passed to
// "sh -c" and Args is ignored.
type ExecSpec struct {
	Command string
	Args    []string
	Shell   bool
	Dir     string
	Env     []string
	Timeout time.Duration
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// ExecResult reports how a command ended. A command killed by Timeout reports
// TimedOut and whatever exit code the kill produced.
type ExecResult struct {
	ExitCode int
	TimedOut bool
}

// ReadOpts bounds a read. MaxBytes of zero means no limit; a positive MaxBytes
// returns at most that many bytes, so callers detect truncation by comparing
// the length with the size reported by Stat.
type ReadOpts struct {
	MaxBytes int64
}

// FileInfo describes one file or directory. Path is workspace-relative and
// slash-separated; Name is the base name.
type FileInfo struct {
	Name    string
	Path    string
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool
}
