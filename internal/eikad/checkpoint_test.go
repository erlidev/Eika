package eikad_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/eikad"
)

// takeCheckpoint posts a checkpoint request and returns the status and body.
func takeCheckpoint(t *testing.T, base, dir, label string) (int, eikad.CheckpointResponse) {
	t.Helper()
	body, err := json.Marshal(eikad.CheckpointRequest{Dir: dir, Run: "run1", Label: label})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	resp := do(t, http.MethodPost, base+"/checkpoint", bytes.NewReader(body))
	var out eikad.CheckpointResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode, out
}

// shadowFiles lists the paths a checkpoint commit holds.
func shadowFiles(t *testing.T, ckpt, commit string) []string {
	t.Helper()
	repos, err := filepath.Glob(filepath.Join(ckpt, "*.git"))
	if err != nil || len(repos) != 1 {
		t.Fatalf("shadow repositories = %v, %v; want exactly one", repos, err)
	}
	out, err := exec.Command("git", "--git-dir", repos[0], "ls-tree", "-r", "--name-only", commit).Output()
	if err != nil {
		t.Fatalf("ls-tree %s: %v", commit, err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

func TestCheckpointRecordsTheTreeAndNothingItShouldSkip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ckpt := filepath.Join(t.TempDir(), "checkpoints")
	base, root := newDaemon(t, eikad.Options{CheckpointDir: ckpt})
	tree := filepath.Join(root, "projects", "app")
	writeTree(t, tree, map[string]string{
		"main.go":                       "package main",
		"gone.txt":                      "will be deleted",
		".gitignore":                    "secret.log\n",
		"secret.log":                    "ignored by the tree",
		"node_modules/dep/index.js":     "a package manager's",
		"vendored/lib.go":               "has a history of its own",
		"big.bin":                       strings.Repeat("x", 10<<20+1),
		"sub/.hidden":                   "dotfiles count",
		"sub/with space and 'quote.txt": "odd names count",
	})

	if out, err := exec.Command("git", "init", "-q", filepath.Join(tree, "vendored")).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	status, start := takeCheckpoint(t, base, "projects/app", eikad.CheckpointStart)
	if status != http.StatusOK || start.Commit == "" {
		t.Fatalf("start checkpoint = %d %+v", status, start)
	}
	if start.Skipped != 1 {
		t.Errorf("skipped = %d, want the one file over 10 MB", start.Skipped)
	}
	got := strings.Join(shadowFiles(t, ckpt, start.Commit), "\n")
	for _, want := range []string{"main.go", "gone.txt", "sub/.hidden", "sub/with space and 'quote.txt", ".gitignore"} {
		if !strings.Contains(got, want) {
			t.Errorf("start commit lacks %s: %s", want, got)
		}
	}
	for _, left := range []string{"secret.log", "node_modules", "big.bin", "vendored"} {
		if strings.Contains(got, left) {
			t.Errorf("start commit holds %s, which a checkpoint leaves out: %s", left, got)
		}
	}

	// An agent's changes (an edit, a deletion, a new file) land in the end
	// commit, and the start commit stays as it was.
	if err := os.Remove(filepath.Join(tree, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeTree(t, tree, map[string]string{"main.go": "package changed", "new.txt": "new"})
	status, end := takeCheckpoint(t, base, "projects/app", eikad.CheckpointEnd)
	if status != http.StatusOK || end.Commit == start.Commit || end.Files != 2+1 {
		t.Fatalf("end checkpoint = %d %+v, want a new commit of 3 changed paths", status, end)
	}
	if got := strings.Join(shadowFiles(t, ckpt, end.Commit), "\n"); strings.Contains(got, "gone.txt") || !strings.Contains(got, "new.txt") {
		t.Errorf("end commit = %s", got)
	}
	if got := strings.Join(shadowFiles(t, ckpt, start.Commit), "\n"); !strings.Contains(got, "gone.txt") {
		t.Errorf("start commit changed after the end checkpoint: %s", got)
	}

	// Nothing in the computer root is the shadow repository.
	if err := filepath.WalkDir(root, func(p string, _ os.DirEntry, _ error) error {
		if strings.HasSuffix(p, ".git") && !strings.Contains(p, "vendored") {
			t.Errorf("the computer root holds a git directory at %s", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointRefusesTheRootAndRecoversFromAKilledDaemon(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ckpt := filepath.Join(t.TempDir(), "checkpoints")
	base, root := newDaemon(t, eikad.Options{CheckpointDir: ckpt})
	if status, _ := takeCheckpoint(t, base, ".", eikad.CheckpointStart); status != http.StatusBadRequest {
		t.Errorf("checkpoint of the root = %d, want 400", status)
	}
	if status, _ := takeCheckpoint(t, base, "../elsewhere", eikad.CheckpointStart); status != http.StatusForbidden {
		t.Errorf("checkpoint outside the root = %d, want 403", status)
	}

	writeTree(t, filepath.Join(root, "app"), map[string]string{"a.txt": "a"})
	if status, _ := takeCheckpoint(t, base, "app", eikad.CheckpointStart); status != http.StatusOK {
		t.Fatalf("first checkpoint = %d", status)
	}
	// A daemon killed inside git leaves its index lock behind. A restarted
	// daemon, which has no git running, clears it rather than failing for good.
	repos, _ := filepath.Glob(filepath.Join(ckpt, "*.git"))
	if len(repos) != 1 {
		t.Fatalf("repositories = %v", repos)
	}
	if err := os.WriteFile(filepath.Join(repos[0], "index.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, _ := newDaemon(t, eikad.Options{Root: root, CheckpointDir: ckpt})
	writeTree(t, filepath.Join(root, "app"), map[string]string{"b.txt": "b"})
	status, resp := takeCheckpoint(t, restarted, "app", eikad.CheckpointEnd)
	if status != http.StatusOK || resp.Files != 1 {
		t.Errorf("checkpoint after a killed daemon = %d %+v, want 200 and the one new file", status, resp)
	}
}
