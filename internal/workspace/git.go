package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/erlidev/eika/internal/executor"
)

// GitUserName and GitUserEmail are the git identity commits the harness makes
// inside a workspace carry when its repository names none of its own.
const (
	GitUserName  = "Eika Agent"
	GitUserEmail = "agent@eika.local"
)

// ErrBadBranch reports a branch name git would not accept.
var ErrBadBranch = errors.New("invalid branch name")

// CheckBranch rejects a branch name git would not accept, so that a name can
// never be anything but a name. git itself is the authority on the rules.
func CheckBranch(ctx context.Context, ex executor.Executor, branch string) error {
	if branch == "" {
		return nil
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("%w: %q", ErrBadBranch, branch)
	}
	if _, err := git(ctx, ex, "check-ref-format", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("%w: %q", ErrBadBranch, branch)
	}
	return nil
}

// git runs one git command in the workspace root and returns its standard
// output. A non-zero exit is an error carrying the command's standard error.
func git(ctx context.Context, ex executor.Executor, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	res, err := ex.Exec(ctx, executor.ExecSpec{
		Command: "git",
		Args:    args,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("run git %s: %w", args[0], err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("run git %s: exit %d: %s", args[0], res.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
