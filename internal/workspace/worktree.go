package workspace

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/erlidev/eika/internal/executor"
)

// worktreesDir is where a workspace keeps the worktrees of the workspaces it
// holds, relative to its root. It sits inside the holder's tree because eikad
// serves nothing outside Root, and it ignores itself, so the holder's status
// and commits never list a worktree.
const worktreesDir = ".eika/worktrees"

// WorktreeDir is where a worktree workspace's files are, relative to Root,
// inside the container of the workspace that holds it.
func WorktreeDir(id string) string { return path.Join(worktreesDir, id) }

// Locate reports where workspace id's files are: in its own container, or,
// when worktreeOf names the workspace holding it, in its worktree in that
// workspace's container, whose daemon, tokens, and state it then shares. in
// is the host, or a test's stand-in for it.
func Locate(ctx context.Context, in interface {
	Inspect(ctx context.Context, id string) (Workspace, error)
}, id, worktreeOf string) (Workspace, error) {
	if worktreeOf == "" {
		return in.Inspect(ctx, id)
	}
	holder, err := in.Inspect(ctx, worktreeOf)
	if err != nil {
		return Workspace{}, err
	}
	holder.Dir = WorktreeDir(id)
	return holder, nil
}

// AddWorktree checks branch out at commit in a new worktree for workspace id,
// which then shares the holder's container and repository: its commits are
// in the holder's repository the moment they are made. ex is the holder's
// executor at its root. The branch must not exist yet.
func AddWorktree(ctx context.Context, ex executor.Executor, id, branch, commit string) error {
	if commit == "" {
		return fmt.Errorf("add worktree for workspace %s: no commit to start from", id)
	}
	if branch == "" {
		return fmt.Errorf("%w: a worktree needs a branch of its own", ErrBadBranch)
	}
	if err := CheckBranch(ctx, ex, branch); err != nil {
		return err
	}
	// An ignore file in the directory itself, rather than an entry in
	// .git/info/exclude, touches nothing a local project's checkout owns.
	if err := ex.WriteFile(ctx, path.Join(worktreesDir, ".gitignore"), []byte("*\n")); err != nil {
		return fmt.Errorf("add worktree for workspace %s: %w", id, err)
	}
	// Relative links keep a local project's worktree usable from the host,
	// where the container's paths do not exist. A git older than 2.48 ignores
	// the setting and links absolutely, which still works in the container.
	if _, err := git(ctx, ex, "-c", "worktree.useRelativePaths=true",
		"worktree", "add", "-b", branch, WorktreeDir(id), commit); err != nil {
		return fmt.Errorf("add worktree for workspace %s: %w", id, err)
	}
	return nil
}

// RemoveWorktree removes workspace id's worktree with every file in it,
// uncommitted ones too. Its branch stays: what was committed there is still
// in the repository. ex is the holder's executor at its root.
func RemoveWorktree(ctx context.Context, ex executor.Executor, id string) error {
	_, err := git(ctx, ex, "worktree", "remove", "--force", WorktreeDir(id))
	if err == nil {
		return nil
	}
	// A directory that is already gone leaves only git's record of it,
	// which prune clears.
	if _, statErr := ex.Stat(ctx, WorktreeDir(id)); !errors.Is(statErr, executor.ErrNotFound) {
		return fmt.Errorf("remove worktree of workspace %s: %w", id, err)
	}
	if _, err := git(ctx, ex, "worktree", "prune"); err != nil {
		return fmt.Errorf("remove worktree of workspace %s: %w", id, err)
	}
	return nil
}
