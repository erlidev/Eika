package repo

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/memory"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Repository provides fixed operations on memory files through an executor.
type Repository struct {
	executor executor.Executor
}

// NewRepository uses an executor rooted at the memory repository.
func NewRepository(ex executor.Executor) *Repository { return &Repository{executor: ex} }

func memoryFilename(name string) (string, error) {
	if !slugPattern.MatchString(name) {
		return "", fmt.Errorf("invalid memory name")
	}
	return name + ".md", nil
}

// Init creates the repository if it does not already exist.
func (r *Repository) Init(ctx context.Context) error {
	_, err := r.git(ctx, "init", "--quiet")
	return err
}

// Read reads one named memory file.
func (r *Repository) Read(ctx context.Context, name string) ([]byte, error) {
	filename, err := memoryFilename(name)
	if err != nil {
		return nil, err
	}
	data, err := r.executor.ReadFile(ctx, filename, executor.ReadOpts{MaxBytes: memory.MaxFileBytes + 1})
	if err != nil {
		return nil, err
	}
	if len(data) > memory.MaxFileBytes {
		return nil, fmt.Errorf("memory file exceeds size limit")
	}
	return data, nil
}

// Write replaces one named memory file.
func (r *Repository) Write(ctx context.Context, name string, data []byte) error {
	filename, err := memoryFilename(name)
	if err != nil {
		return err
	}
	if len(data) > memory.MaxFileBytes {
		return fmt.Errorf("memory file exceeds size limit")
	}
	return r.executor.WriteFile(ctx, filename, data)
}

// Commit stages and commits one named memory file as Eika.
func (r *Repository) Commit(ctx context.Context, name string) error {
	filename, err := memoryFilename(name)
	if err != nil {
		return err
	}
	if _, err := r.git(ctx, "add", "--", filename); err != nil {
		return err
	}
	_, err = r.git(ctx, "-c", "user.name=Eika", "-c", "user.email=eika@localhost", "commit", "--quiet", "--only", "-m", "Update memory", "--", filename)
	return err
}

// Status returns the repository's short status.
func (r *Repository) Status(ctx context.Context) (string, error) {
	return r.git(ctx, "status", "--short", "--untracked-files=normal")
}

func (r *Repository) git(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	result, err := r.executor.Exec(ctx, executor.ExecSpec{
		Command: "git", Args: args, Timeout: 30 * time.Second,
		Env:    []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=Eika", "GIT_AUTHOR_EMAIL=eika@localhost", "GIT_COMMITTER_NAME=Eika", "GIT_COMMITTER_EMAIL=eika@localhost"},
		Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("run git: %w", err)
	}
	if result.ExitCode != 0 || result.TimedOut {
		return "", fmt.Errorf("run git: exit %d: %s", result.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
