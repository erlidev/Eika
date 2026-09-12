package workspace

import (
	"bytes"
	"context"
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

// Clone checks the project out into a running workspace from the hub and
// returns the commit the workspace starts from. An empty project repository
// yields an empty base commit and an unborn branch.
func (h *Host) Clone(ctx context.Context, ws Workspace, project, branch string) (string, error) {
	if _, err := h.opts.Hub.Init(ctx, project); err != nil {
		return "", err
	}
	ex, err := h.Executor(ws)
	if err != nil {
		return "", err
	}
	url := strings.TrimSuffix(h.opts.HubURL, "/") + hub.Prefix + "/" + project + ".git"

	setup := []string{
		"git config --global credential.helper '" + workspaceCredentialHelper + "'",
		"git config --global user.name '" + gitUserName + "'",
		"git config --global user.email '" + gitUserEmail + "'",
		"git clone " + url + " .",
	}
	for _, script := range setup {
		if _, err := run(ctx, ex, script); err != nil {
			return "", err
		}
	}
	if branch != "" {
		// A repository with no commits has no branch to switch to, so the
		// unborn HEAD is pointed at the branch instead.
		if _, err := run(ctx, ex, "git checkout -B "+branch); err != nil {
			if _, err := run(ctx, ex, "git symbolic-ref HEAD refs/heads/"+branch); err != nil {
				return "", err
			}
		}
	}
	head, err := run(ctx, ex, "git rev-parse HEAD")
	if err != nil {
		// An unborn branch has no HEAD; that is a base commit of "".
		return "", nil
	}
	h.log.Info("workspace cloned", "workspace_id", ws.ID, "project", project, "base_commit", head)
	return head, nil
}

// run executes a shell script in the workspace root and returns its standard
// output. A non-zero exit is an error carrying the command's standard error.
func run(ctx context.Context, ex executor.Executor, script string) (string, error) {
	var stdout, stderr bytes.Buffer
	res, err := ex.Exec(ctx, executor.ExecSpec{
		Command: script,
		Shell:   true,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("run %q: %w", script, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("run %q: exit %d: %s", script, res.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
