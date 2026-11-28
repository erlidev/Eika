package eikad

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Bounds on a checkpoint.
const (
	// maxCheckpointFile is the largest file a checkpoint keeps. A larger file
	// is left out, so a download or a model weight does not fill the disk.
	maxCheckpointFile = 10 << 20
	// checkpointTimeout bounds one git command, so a checkpoint cannot hang
	// the run that waits for it.
	checkpointTimeout = 5 * time.Minute
	// maxCheckpointRequest caps the body of POST /checkpoint.
	maxCheckpointRequest = 4 << 10
)

// checkpointExclude is the shadow repository's own ignore list: the
// directories package managers fill, which a project can fetch again. The
// tree's own .gitignore files apply as well.
const checkpointExclude = `node_modules/
bower_components/
.pnpm-store/
.npm/
.yarn/cache/
.venv/
venv/
__pycache__/
.tox/
.gradle/
.m2/
.cargo/registry/
`

// runPattern is what a run id may look like: it ends up in a commit message.
var runPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// checkpointRepo is the state kept for one shadow repository.
type checkpointRepo struct {
	// mu serialises checkpoints of one tree, since git locks its index.
	mu sync.Mutex
	// swept is set once the repository's stale locks have been removed.
	swept bool
}

// repo returns the state for one shadow repository.
func (d *Daemon) repo(gitDir string) *checkpointRepo {
	d.ckptMu.Lock()
	defer d.ckptMu.Unlock()
	r := d.ckptRepos[gitDir]
	if r == nil {
		r = &checkpointRepo{}
		d.ckptRepos[gitDir] = r
	}
	return r
}

// handleCheckpoint commits a project tree to its shadow repository. The
// repository is in the daemon's own directory, outside the computer root, so
// no path a file tool can name reaches it.
func (d *Daemon) handleCheckpoint(w http.ResponseWriter, r *http.Request) {
	var req CheckpointRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCheckpointRequest)).Decode(&req); err != nil {
		writeError(w, d.log, http.StatusBadRequest, fmt.Errorf("decode checkpoint request: %w", err))
		return
	}
	if req.Label != CheckpointStart && req.Label != CheckpointEnd {
		writeError(w, d.log, http.StatusBadRequest, fmt.Errorf("checkpoint label %q is not %s or %s", req.Label, CheckpointStart, CheckpointEnd))
		return
	}
	if !runPattern.MatchString(req.Run) {
		writeError(w, d.log, http.StatusBadRequest, errors.New("checkpoint run id is not a plain id"))
		return
	}
	tree, err := d.resolve(req.Dir)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}
	if tree == d.root {
		writeError(w, d.log, http.StatusBadRequest, errors.New("a checkpoint covers a project tree, not the computer root"))
		return
	}
	if fi, err := os.Stat(tree); err != nil || !fi.IsDir() {
		writeError(w, d.log, http.StatusBadRequest, fmt.Errorf("checkpoint tree %s is not a directory", req.Dir))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), checkpointTimeout)
	defer cancel()
	resp, err := d.checkpoint(ctx, tree, req)
	if err != nil {
		d.log.Error("checkpoint", "dir", req.Dir, "run", req.Run, "label", req.Label, "error", err)
		writeError(w, d.log, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, d.log, http.StatusOK, resp)
}

// checkpoint stages what changed in tree and commits it, and reports how long
// that took.
func (d *Daemon) checkpoint(ctx context.Context, tree string, req CheckpointRequest) (CheckpointResponse, error) {
	began := time.Now()
	sum := sha256.Sum256([]byte(tree))
	gitDir := filepath.Join(d.ckptDir, hex.EncodeToString(sum[:8])+".git")
	repo := d.repo(gitDir)
	repo.mu.Lock()
	defer repo.mu.Unlock()

	g := gitRunner{gitDir: gitDir, tree: tree}
	if err := d.prepareRepo(ctx, g, repo); err != nil {
		return CheckpointResponse{}, err
	}
	out, err := g.run(ctx, nil, "ls-files", "-z", "--others", "--modified", "--deleted", "--exclude-standard")
	if err != nil {
		return CheckpointResponse{}, err
	}
	paths, skipped := keepable(tree, out)
	if len(paths) > 0 {
		if _, err := g.run(ctx, bytes.NewReader(paths.nul()), "add", "-A", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			return CheckpointResponse{}, err
		}
	}
	msg := fmt.Sprintf("run %s %s", req.Run, req.Label)
	if _, err := g.run(ctx, nil, "commit", "-q", "--allow-empty", "-m", msg); err != nil {
		return CheckpointResponse{}, err
	}
	head, err := g.run(ctx, nil, "rev-parse", "HEAD")
	if err != nil {
		return CheckpointResponse{}, err
	}
	return CheckpointResponse{
		Commit:  strings.TrimSpace(string(head)),
		Files:   len(paths),
		Skipped: skipped,
		TookMS:  time.Since(began).Milliseconds(),
	}, nil
}

// prepareRepo makes the shadow repository exist, clears the locks a daemon
// that was killed mid-checkpoint left behind (the first time this process
// uses the repository, when no git of ours can be running), and writes its
// exclude list.
func (d *Daemon) prepareRepo(ctx context.Context, g gitRunner, repo *checkpointRepo) error {
	if err := os.MkdirAll(d.ckptDir, 0o700); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}
	if !repo.swept {
		for _, lock := range []string{"index.lock", "HEAD.lock", "refs/heads/main.lock"} {
			if err := os.Remove(filepath.Join(g.gitDir, lock)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale checkpoint lock: %w", err)
			}
		}
		repo.swept = true
	}
	if _, err := os.Stat(filepath.Join(g.gitDir, "HEAD")); os.IsNotExist(err) {
		if _, err := g.run(ctx, nil, "init", "-q", "--initial-branch=main"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(g.gitDir, "info"), 0o700); err != nil {
		return fmt.Errorf("create checkpoint exclude directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(g.gitDir, "info", "exclude"), []byte(checkpointExclude), 0o600); err != nil {
		return fmt.Errorf("write checkpoint exclude list: %w", err)
	}
	return nil
}

// pathList is the paths one checkpoint stages.
type pathList []string

// nul renders the list the way git's --pathspec-file-nul reads it.
func (p pathList) nul() []byte {
	return []byte(strings.Join(p, "\x00") + "\x00")
}

// keepable reads git's NUL-separated list of changed paths and drops what a
// checkpoint leaves out: a repository nested in the tree (it has its own
// history) and a file over the size limit. A deleted path no longer exists
// and is kept, so the deletion is recorded. It reports how many files were
// left out for size.
func keepable(tree string, listing []byte) (pathList, int) {
	var out pathList
	var skipped int
	seen := map[string]bool{}
	for _, p := range strings.Split(string(listing), "\x00") {
		if p == "" || strings.HasSuffix(p, "/") || seen[p] {
			continue
		}
		seen[p] = true
		if fi, err := os.Lstat(filepath.Join(tree, p)); err == nil && fi.Mode().IsRegular() && fi.Size() > maxCheckpointFile {
			skipped++
			continue
		}
		out = append(out, p)
	}
	return out, skipped
}

// gitRunner runs git against one shadow repository and the tree it follows.
type gitRunner struct {
	gitDir, tree string
}

// run runs one git command and returns its output. The environment is
// pared down so nothing in the tree or the home directory can change what
// git does: no global configuration, no hooks, no prompts.
func (g gitRunner) run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	full := append([]string{
		"--git-dir=" + g.gitDir, "--work-tree=" + g.tree, "--literal-pathspecs",
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"-c", "core.autocrlf=false", "-c", "core.safecrlf=false",
		"-c", "gc.autoDetach=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = g.tree
	cmd.Stdin = stdin
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"GIT_AUTHOR_NAME=Eika", "GIT_AUTHOR_EMAIL=eika@localhost",
		"GIT_COMMITTER_NAME=Eika", "GIT_COMMITTER_EMAIL=eika@localhost",
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
