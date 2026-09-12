//go:build !prod

package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/erlidev/eika/internal/executor"
)

// Executor runs commands and file operations in a directory on the harness
// filesystem. It is the test-only implementation of executor.Executor.
type Executor struct {
	root string
}

// New returns an Executor rooted at dir, which must exist. Symlinks in dir are
// resolved once so that later containment checks compare real paths.
func New(dir string) (*Executor, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", dir, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", dir, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("stat root %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root %q is not a directory", dir)
	}
	return &Executor{root: real}, nil
}

// Root reports the directory that acts as the workspace root.
func (e *Executor) Root() string { return e.root }

// Exec runs a command with its working directory inside the root, streaming
// output to the writers in spec. A non-zero exit is reported in the result,
// not as an error; only a command that could not run at all returns an error.
func (e *Executor) Exec(ctx context.Context, spec executor.ExecSpec) (executor.ExecResult, error) {
	dir := e.root
	if spec.Dir != "" {
		resolved, err := e.resolve(spec.Dir)
		if err != nil {
			return executor.ExecResult{}, fmt.Errorf("exec: %w", err)
		}
		dir = resolved
	}
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	var cmd *exec.Cmd
	if spec.Shell {
		cmd = exec.CommandContext(ctx, "sh", "-c", spec.Command)
	} else {
		cmd = exec.CommandContext(ctx, spec.Command, spec.Args...)
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Stdin = spec.Stdin
	cmd.Stdout = writerOrDiscard(spec.Stdout)
	cmd.Stderr = writerOrDiscard(spec.Stderr)

	err := cmd.Run()
	res := executor.ExecResult{TimedOut: spec.Timeout > 0 && errors.Is(ctx.Err(), context.DeadlineExceeded)}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	case res.TimedOut:
		res.ExitCode = -1
	default:
		return executor.ExecResult{}, fmt.Errorf("run %s: %w", spec.Command, err)
	}
	return res, nil
}

// ReadFile returns the contents of path, at most opts.MaxBytes when positive.
func (e *Executor) ReadFile(_ context.Context, path string, opts executor.ReadOpts) ([]byte, error) {
	abs, err := e.resolve(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", path, err)
	}
	defer f.Close()
	var r io.Reader = f
	if opts.MaxBytes > 0 {
		r = io.LimitReader(f, opts.MaxBytes)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", path, err)
	}
	return data, nil
}

// WriteFile writes data to path, creating parent directories as needed.
func (e *Executor) WriteFile(_ context.Context, path string, data []byte) error {
	abs, err := e.resolve(path)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create directory for %q: %w", path, err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("write file %q: %w", path, err)
	}
	return nil
}

// Stat describes one path.
func (e *Executor) Stat(_ context.Context, path string) (executor.FileInfo, error) {
	abs, err := e.resolve(path)
	if err != nil {
		return executor.FileInfo{}, fmt.Errorf("stat: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return executor.FileInfo{}, fmt.Errorf("stat %q: %w", path, err)
	}
	return e.fileInfo(abs, info)
}

// List describes the direct children of a directory, sorted by name as the
// filesystem reports them.
func (e *Executor) List(_ context.Context, path string) ([]executor.FileInfo, error) {
	abs, err := e.resolve(path)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("list %q: %w", path, err)
	}
	out := make([]executor.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("list %q: %w", path, err)
		}
		fi, err := e.fileInfo(filepath.Join(abs, entry.Name()), info)
		if err != nil {
			return nil, err
		}
		out = append(out, fi)
	}
	return out, nil
}

// fileInfo converts an os.FileInfo into the workspace-relative form.
func (e *Executor) fileInfo(abs string, info os.FileInfo) (executor.FileInfo, error) {
	rel, err := executor.Rel(e.root, abs)
	if err != nil {
		return executor.FileInfo{}, err
	}
	return executor.FileInfo{
		Name:    info.Name(),
		Path:    rel,
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime().UTC(),
		IsDir:   info.IsDir(),
	}, nil
}

// resolve validates a workspace-relative path lexically and again after
// symlink evaluation, so a symlink inside the root cannot point out of it.
func (e *Executor) resolve(path string) (string, error) {
	abs, err := executor.Resolve(e.root, path)
	if err != nil {
		return "", err
	}
	real, err := evalExisting(abs)
	if err != nil {
		return "", err
	}
	if _, err := executor.Resolve(e.root, real); err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, executor.ErrPathOutsideRoot)
	}
	return abs, nil
}

// evalExisting resolves symlinks in the longest existing prefix of abs and
// re-appends the part that does not exist yet, so a path being created is
// checked against the real location of its parent.
func evalExisting(abs string) (string, error) {
	rest := ""
	for p := abs; ; {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(real, rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve symlinks in %q: %w", abs, err)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// writerOrDiscard keeps a nil writer from panicking os/exec.
func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

var _ executor.Executor = (*Executor)(nil)
