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

// hubRemote is the name of the git remote a workspace reaches the hub on. It
// is not "origin": a local project's checkout already has an origin of the
// user's own, and the hub must not displace it.
const hubRemote = "eika-hub"

// ErrBadBranch reports a branch name git would not accept.
var ErrBadBranch = errors.New("invalid branch name")

// ErrNoRepository reports that a workspace holds no git repository, which a
// local project's directory need not.
var ErrNoRepository = errors.New("workspace holds no git repository")

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
	if err := CheckBranch(ctx, ex, branch); err != nil {
		return "", err
	}
	if err := h.opts.Hub.Grant(ws.ID, project, ws.HubToken); err != nil {
		return "", err
	}
	url := strings.TrimSuffix(h.opts.HubURL, "/") + hub.Prefix + "/" + project + ".git"

	// Every argument is passed as an argument, never through a shell, so a
	// project or branch name cannot become a command.
	if err := configureGit(ctx, ex); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"clone", url, "."}, {"remote", "add", hubRemote, url}} {
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

// CloneAt checks the project out into a running workspace from the hub and
// puts it on branch at commit, which is how a child workspace starts where
// its parent stood. An empty commit leaves the clone's own HEAD in place.
//
// The commit must already be in the hub: push the workspace it came from
// first.
func (h *Host) CloneAt(ctx context.Context, ws Workspace, project, branch, commit string) (string, error) {
	if _, err := h.Clone(ctx, ws, project, ""); err != nil {
		return "", err
	}
	ex, err := h.Executor(ws)
	if err != nil {
		return "", err
	}
	if err := CheckBranch(ctx, ex, branch); err != nil {
		return "", err
	}
	args := []string{"checkout", "-B", branch}
	if commit != "" {
		args = append(args, commit)
	}
	if _, err := git(ctx, ex, args...); err != nil {
		// A repository with no commits is the one checkout that is allowed to
		// fail: it has no HEAD to branch from, so the unborn HEAD is pointed
		// at the branch instead. Anything else, a commit the hub does not
		// have above all, is the caller's to hear about.
		if _, empty := git(ctx, ex, "rev-parse", "--verify", "HEAD"); empty == nil || commit != "" {
			return "", fmt.Errorf("check out %s in workspace %s: %w", branch, ws.ID, err)
		}
		if _, err := git(ctx, ex, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
			return "", err
		}
		return "", nil
	}
	head, err := git(ctx, ex, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	h.log.Info("workspace cloned at commit",
		"workspace_id", ws.ID, "project", project, "branch", branch, "base_commit", head)
	return head, nil
}

// Push sends a branch from a workspace to the project's repository in the
// hub, creating that repository if the project has none yet. A local
// project's workspace pushes the same way: the hub mirrors local projects, so
// that forks and subagents work alike for both kinds.
//
// The push never forces: a branch in the hub belongs to whoever created it,
// and a push that cannot fast-forward is a divergence the caller has to
// resolve rather than overwrite.
func (h *Host) Push(ctx context.Context, ws Workspace, project, branch string) error {
	ex, err := h.connectHub(ctx, ws, project)
	if err != nil {
		return err
	}
	if err := CheckBranch(ctx, ex, branch); err != nil {
		return err
	}
	if _, err := git(ctx, ex, "push", hubRemote, "HEAD:refs/heads/"+branch); err != nil {
		return err
	}
	h.log.Info("workspace pushed to the hub", "workspace_id", ws.ID, "project", project, "branch", branch)
	return nil
}

// Fetch brings a branch from the project's repository in the hub into a
// workspace, where it is then reachable as FETCH_HEAD. It is what a merge
// between two workspaces starts with.
func (h *Host) Fetch(ctx context.Context, ws Workspace, project, branch string) error {
	ex, err := h.connectHub(ctx, ws, project)
	if err != nil {
		return err
	}
	if err := CheckBranch(ctx, ex, branch); err != nil {
		return err
	}
	if _, err := git(ctx, ex, "fetch", hubRemote, branch); err != nil {
		return err
	}
	return nil
}

// connectHub makes sure a running workspace can talk to its project in the
// hub: the repository exists, the workspace holds a grant for it, git knows
// who it commits as and how to answer the hub's challenge, and the hub is a
// remote. Every step is idempotent, so any of Clone, Push, and Fetch may be
// the first one a workspace runs.
func (h *Host) connectHub(ctx context.Context, ws Workspace, project string) (executor.Executor, error) {
	if _, err := h.opts.Hub.Init(ctx, project); err != nil {
		return nil, err
	}
	ex, err := h.Executor(ws)
	if err != nil {
		return nil, err
	}
	if _, err := git(ctx, ex, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNoRepository, ws.ID)
	}
	if err := h.opts.Hub.Grant(ws.ID, project, ws.HubToken); err != nil {
		return nil, err
	}
	if err := configureGit(ctx, ex); err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(h.opts.HubURL, "/") + hub.Prefix + "/" + project + ".git"
	if _, err := git(ctx, ex, "remote", "set-url", hubRemote, url); err != nil {
		if _, err := git(ctx, ex, "remote", "add", hubRemote, url); err != nil {
			return nil, err
		}
	}
	return ex, nil
}

// configureGit gives a workspace the identity its commits carry and the
// credential helper the hub's challenge is answered from.
func configureGit(ctx context.Context, ex executor.Executor) error {
	settings := [][]string{
		{"config", "--global", "credential.helper", workspaceCredentialHelper},
		{"config", "--global", "user.name", gitUserName},
		{"config", "--global", "user.email", gitUserEmail},
	}
	for _, args := range settings {
		if _, err := git(ctx, ex, args...); err != nil {
			return err
		}
	}
	return nil
}

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
