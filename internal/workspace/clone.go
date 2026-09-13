package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/workspace/hub"
)

// The git identity commits made inside a workspace carry. The user's own
// identity belongs to the remote, which the harness pushes to.
const (
	gitUserName  = "Eika Agent"
	gitUserEmail = "agent@eika.local"
)

// workspaceCredentialHelper answers the hub's basic auth challenge from the
// container's environment, so that the token never lands in .git/config or in
// the output of `git remote -v`.
const workspaceCredentialHelper = `!f() { echo "username=${` + hubUserEnv + `}"; echo "password=${` + hubTokenEnv + `}"; }; f`

// ErrBadBranch reports a branch name git would not accept.
var ErrBadBranch = errors.New("invalid branch name")

// Clone checks the project out into a running workspace from the hub and
// returns the commit the workspace starts from. An empty project repository
// yields an empty base commit and an unborn branch.
//
// The project must be the one the workspace was created for: a workspace can
// only reach its own project on the hub.
func (h *Host) Clone(ctx context.Context, ws Workspace, project, branch string) (string, error) {
	// Init validates the project name and is a no-op once the repository
	// exists, so a first clone creates the project.
	if _, err := h.opts.Hub.Init(ctx, project); err != nil {
		return "", err
	}
	ex, err := h.Executor(ws)
	if err != nil {
		return "", err
	}
	if err := checkBranch(ctx, ex, branch); err != nil {
		return "", err
	}
	if err := h.opts.Hub.Grant(ws.ID, project, ws.HubToken); err != nil {
		return "", err
	}
	url := strings.TrimSuffix(h.opts.HubURL, "/") + hub.Prefix + "/" + project + ".git"

	// Every argument is passed as an argument, never through a shell, so a
	// project or branch name cannot become a command.
	setup := [][]string{
		{"config", "--global", "credential.helper", workspaceCredentialHelper},
		{"config", "--global", "user.name", gitUserName},
		{"config", "--global", "user.email", gitUserEmail},
		{"clone", url, "."},
	}
	for _, args := range setup {
		if _, err := git(ctx, ex, args...); err != nil {
			return "", err
		}
	}
	if branch != "" {
		// A repository with no commits has no branch to switch to, so the
		// unborn HEAD is pointed at the branch instead.
		if _, err := git(ctx, ex, "checkout", "-B", branch); err != nil {
			if _, err := git(ctx, ex, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
				return "", err
			}
		}
	}
	head, err := git(ctx, ex, "rev-parse", "HEAD")
	if err != nil {
		// An unborn branch has no HEAD; that is a base commit of "".
		return "", nil
	}
	h.log.Info("workspace cloned", "workspace_id", ws.ID, "project", project, "base_commit", head)
	return head, nil
}

// checkBranch rejects a branch name git would not accept, so that a name can
// never be anything but a name. git itself is the authority on the rules.
func checkBranch(ctx context.Context, ex executor.Executor, branch string) error {
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
