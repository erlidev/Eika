package hub_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/workspace/hub"
)

// requireGit skips a test that cannot run without the git binary.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// newHub builds a hub over a temporary directory.
func newHub(t *testing.T) *hub.Hub {
	t.Helper()
	h, err := hub.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new hub: %v", err)
	}
	return h
}

// git runs a git command in dir and fails the test if it does not succeed.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPathRejectsUnusableNames(t *testing.T) {
	h := newHub(t)
	for _, name := range []string{"", "..", "a/b", "../escape", ".hidden", strings.Repeat("x", 200)} {
		if _, err := h.Path(name); !errors.Is(err, hub.ErrBadProject) {
			t.Errorf("Path(%q) error = %v, want ErrBadProject", name, err)
		}
	}
	if _, err := h.Path("eika-1_2"); err != nil {
		t.Errorf("Path rejected a usable name: %v", err)
	}
}

func TestInitIsIdempotentAndListable(t *testing.T) {
	requireGit(t)
	h := newHub(t)

	path, err := h.Init(t.Context(), "demo")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err != nil {
		t.Fatalf("the repository was not created: %v", err)
	}
	if _, err := h.Init(t.Context(), "demo"); err != nil {
		t.Fatalf("second init: %v", err)
	}

	projects, err := h.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projects) != 1 || projects[0] != "demo" {
		t.Errorf("list = %v, want [demo]", projects)
	}
}

func TestHandlerRequiresAGrantedToken(t *testing.T) {
	requireGit(t)
	h := newHub(t)
	if _, err := h.Init(t.Context(), "demo"); err != nil {
		t.Fatalf("init: %v", err)
	}
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)

	get := func(user, token, path string) int {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		if user != "" {
			req.SetBasicAuth(user, token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	refs := "/git/demo.git/info/refs?service=git-upload-pack"
	if code := get("", "", refs); code != http.StatusUnauthorized {
		t.Errorf("anonymous status = %d, want 401", code)
	}
	if code := get("ws1", "wrong", refs); code != http.StatusUnauthorized {
		t.Errorf("wrong token status = %d, want 401", code)
	}

	h.Grant("ws1", "token")
	if code := get("ws1", "token", refs); code != http.StatusOK {
		t.Errorf("granted status = %d, want 200", code)
	}
	if code := get("ws1", "token", "/git/absent.git/info/refs?service=git-upload-pack"); code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", code)
	}
	if code := get("ws1", "token", "/git/../etc.git/info/refs"); code != http.StatusNotFound {
		t.Errorf("traversal status = %d, want 404", code)
	}

	h.Revoke("ws1")
	if code := get("ws1", "token", refs); code != http.StatusUnauthorized {
		t.Errorf("revoked status = %d, want 401", code)
	}
}

func TestCloneAndPushOverHTTP(t *testing.T) {
	requireGit(t)
	h := newHub(t)
	if _, err := h.Init(t.Context(), "demo"); err != nil {
		t.Fatalf("init: %v", err)
	}
	h.Grant("ws1", "token")
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)

	url := strings.Replace(srv.URL, "http://", "http://ws1:token@", 1) + "/git/demo.git"
	work := t.TempDir()
	git(t, work, "clone", url, ".")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hei"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	git(t, work, "add", "README.md")
	git(t, work, "commit", "-m", "first")
	git(t, work, "push", "origin", "HEAD:refs/heads/main")

	// A second workspace sees the push.
	other := t.TempDir()
	git(t, other, "clone", url, ".")
	data, err := os.ReadFile(filepath.Join(other, "README.md"))
	if err != nil || string(data) != "hei" {
		t.Errorf("second clone = %q, %v, want hei", data, err)
	}
}

func TestMirrorFetchesAndPushesARemote(t *testing.T) {
	requireGit(t)
	h := newHub(t)

	// A bare repository on disk stands in for the upstream remote.
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, t.TempDir(), "init", "--bare", "--initial-branch=main", remote)
	seed := t.TempDir()
	git(t, seed, "clone", remote, ".")
	if err := os.WriteFile(filepath.Join(seed, "file.txt"), []byte("upstream"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	git(t, seed, "add", "file.txt")
	git(t, seed, "commit", "-m", "upstream commit")
	git(t, seed, "push", "origin", "HEAD:refs/heads/main")

	creds := hub.Credentials{Username: "x-access-token", Password: "unused-for-a-local-remote"}
	if err := h.Mirror(t.Context(), "demo", remote, creds); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	path, err := h.Path("demo")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if got := git(t, path, "log", "-1", "--format=%s", "main"); got != "upstream commit" {
		t.Errorf("mirrored log = %q, want the upstream commit", got)
	}

	// A branch created in the hub can be pushed back to the remote.
	git(t, path, "branch", "work", "main")
	if err := h.Push(t.Context(), "demo", remote, "refs/heads/work:refs/heads/work", creds); err != nil {
		t.Fatalf("push: %v", err)
	}
	if got := git(t, remote, "rev-parse", "work"); got == "" {
		t.Error("the remote did not receive the branch")
	}
}

func TestPushRejectsAnUnknownProject(t *testing.T) {
	h := newHub(t)
	err := h.Push(t.Context(), "absent", "http://remote", "refs/heads/main:refs/heads/main", hub.Credentials{})
	if !errors.Is(err, hub.ErrNoProject) {
		t.Errorf("Push error = %v, want ErrNoProject", err)
	}
}
