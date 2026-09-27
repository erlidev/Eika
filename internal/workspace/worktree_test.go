package workspace_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/workspace"
)

// gitIn runs git in dir with an identity of its own, so that the machine
// running the tests needs no git configuration.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Eika Test", "-c", "user.email=test@eika.local",
		"-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// holderRepo returns a repository with one commit, as a holding workspace's
// root has, and that commit.
func holderRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "first")
	return dir, gitIn(t, dir, "rev-parse", "HEAD")
}

func TestAWorktreeSharesTheHoldersRepository(t *testing.T) {
	dir, head := holderRepo(t)
	ex, err := local.New(dir)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	ctx := t.Context()
	if err := workspace.AddWorktree(ctx, ex, "w1", "main-worker-w1", head); err != nil {
		t.Fatalf("add worktree: %v", err)
	}

	tree := filepath.Join(dir, filepath.FromSlash(workspace.WorktreeDir("w1")))
	if got := gitIn(t, tree, "rev-parse", "--abbrev-ref", "HEAD"); got != "main-worker-w1" {
		t.Errorf("worktree branch = %q, want main-worker-w1", got)
	}
	if _, err := os.Stat(filepath.Join(tree, "README.md")); err != nil {
		t.Errorf("the worktree lacks the commit's files: %v", err)
	}
	// The holder's tree does not list the worktree, so the holder never
	// commits its child's files as its own.
	if status := gitIn(t, dir, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Errorf("holder status = %q, want a clean tree", status)
	}

	// A commit in the worktree is in the holder's repository at once.
	if err := os.WriteFile(filepath.Join(tree, "notes.txt"), []byte("from the child\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitIn(t, tree, "add", "-A")
	gitIn(t, tree, "commit", "-m", "notes")
	if got := gitIn(t, dir, "show", "main-worker-w1:notes.txt"); got != "from the child" {
		t.Errorf("holder reads %q from the branch, want the worktree's commit", got)
	}

	if err := workspace.RemoveWorktree(ctx, ex, "w1"); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if _, err := os.Stat(tree); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree directory after removal: %v, want it gone", err)
	}
	if got := gitIn(t, dir, "show", "main-worker-w1:notes.txt"); got != "from the child" {
		t.Errorf("branch after removal reads %q, want the commit kept", got)
	}
}

func TestRemovingAWorktreeWhoseDirectoryIsGone(t *testing.T) {
	dir, head := holderRepo(t)
	ex, err := local.New(dir)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	ctx := t.Context()
	if err := workspace.AddWorktree(ctx, ex, "w1", "main-w1", head); err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, filepath.FromSlash(workspace.WorktreeDir("w1")))); err != nil {
		t.Fatalf("remove the directory: %v", err)
	}
	if err := workspace.RemoveWorktree(ctx, ex, "w1"); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if list := gitIn(t, dir, "worktree", "list", "--porcelain"); strings.Contains(list, "w1") {
		t.Errorf("worktree list = %q, want the record of w1 gone", list)
	}
}

func TestAddWorktreeRefusesWhatItCannotCheckOut(t *testing.T) {
	dir, head := holderRepo(t)
	ex, err := local.New(dir)
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	cases := map[string]struct {
		branch, commit string
		bad            bool
	}{
		"a branch git refuses": {branch: "bad..name", commit: head, bad: true},
		"no branch":            {branch: "", commit: head, bad: true},
		"no commit":            {branch: "main-w2", commit: ""},
		"a branch that exists": {branch: "main", commit: head},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := workspace.AddWorktree(t.Context(), ex, "w2", c.branch, c.commit)
			if err == nil {
				t.Fatal("add worktree succeeded")
			}
			if errors.Is(err, workspace.ErrBadBranch) != c.bad {
				t.Errorf("error = %v, want ErrBadBranch: %v", err, c.bad)
			}
		})
	}
}

// inspector answers Inspect from a map, standing in for the host.
type inspector map[string]workspace.Workspace

func (in inspector) Inspect(_ context.Context, id string) (workspace.Workspace, error) {
	ws, ok := in[id]
	if !ok {
		return workspace.Workspace{}, workspace.ErrNoWorkspace
	}
	return ws, nil
}

func TestLocateFindsAWorktreeInItsHoldersContainer(t *testing.T) {
	holder := workspace.Workspace{ID: "h1", Address: "http://eika-ws-h1:7000", Token: "t", State: workspace.StateRunning}
	in := inspector{"h1": holder}
	ctx := t.Context()

	own, err := workspace.Locate(ctx, in, "h1", "")
	if err != nil || own != holder {
		t.Errorf("locate the holder = %+v, %v, want its own container", own, err)
	}
	tree, err := workspace.Locate(ctx, in, "w1", "h1")
	if err != nil {
		t.Fatalf("locate the worktree: %v", err)
	}
	if tree.Dir != ".eika/worktrees/w1" {
		t.Errorf("dir = %q, want .eika/worktrees/w1", tree.Dir)
	}
	if tree.ID != holder.ID || tree.Address != holder.Address || tree.State != holder.State {
		t.Errorf("worktree = %+v, want the holder's container", tree)
	}
	if _, err := workspace.Locate(ctx, in, "w2", "gone"); !errors.Is(err, workspace.ErrNoWorkspace) {
		t.Errorf("locate in a missing holder: %v, want ErrNoWorkspace", err)
	}
}
