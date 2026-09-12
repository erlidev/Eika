package executor

import (
	"context"
	"io"
	"io/fs"
	"time"
)

// Executor runs commands and file operations inside one workspace.
type Executor interface {
	// Root is the absolute path of the workspace inside the sandbox.
	Root() string
	// Exec runs a command and streams its output into the spec's writers.
	Exec(ctx context.Context, spec ExecSpec) (ExecResult, error)
	// ReadFile returns the contents of path, bounded by opts.
	ReadFile(ctx context.Context, path string, opts ReadOpts) ([]byte, error)
	// WriteFile writes data to path, creating parent directories.
	WriteFile(ctx context.Context, path string, data []byte) error
	// Stat describes one file.
	Stat(ctx context.Context, path string) (FileInfo, error)
	// List describes the direct children of a directory.
	List(ctx context.Context, path string) ([]FileInfo, error)
}

// ExecSpec describes one command to run.
type ExecSpec struct {
	// Command is the program to run, or the script when Shell is set.
	Command string
	// Args are the arguments passed to Command. Ignored when Shell is set.
	Args []string
	// Shell runs Command through `sh -c` instead of executing it directly.
	Shell bool
	// Dir is the working directory, relative to the root. Empty means root.
	Dir string
	// Env holds additional KEY=VALUE entries for the command.
	Env []string
	// Timeout kills the command after this long. Zero means no limit.
	Timeout time.Duration
	// Stdin, when set, is fed to the command.
	Stdin io.Reader
	// Stdout receives the command's standard output as it is produced.
	Stdout io.Writer
	// Stderr receives the command's standard error as it is produced.
	Stderr io.Writer
}

// ExecResult is the outcome of a finished command.
type ExecResult struct {
	// ExitCode is the process exit status.
	ExitCode int
	// TimedOut reports that the command was killed by its timeout.
	TimedOut bool
}

// ReadOpts bounds a file read.
type ReadOpts struct {
	// MaxBytes caps how much is read. Zero means the implementation default.
	MaxBytes int64
}

// FileInfo describes one file in a workspace.
type FileInfo struct {
	// Name is the base name.
	Name string
	// Path is the path relative to the workspace root.
	Path string
	// Size is the size in bytes.
	Size int64
	// Mode is the file mode and permission bits.
	Mode fs.FileMode
	// ModTime is the modification time, in UTC.
	ModTime time.Time
	// IsDir reports whether the file is a directory.
	IsDir bool
}
