package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Environment variables the credential helper reads. The helper is a shell
// snippet passed on the command line, so the credentials themselves never
// reach a file on disk or the process table of another process.
const (
	credUserEnv     = "EIKA_GIT_USER"
	credPasswordEnv = "EIKA_GIT_PASSWORD"
)

// credentialHelper prints the credentials git asks for from the environment.
// The empty helper before it clears any helper inherited from a global
// gitconfig.
const credentialHelper = `!f() { echo "username=${` + credUserEnv + `}"; echo "password=${` + credPasswordEnv + `}"; }; f`

// projectName is the accepted shape of a project name. It is also the name of
// a directory under the hub root, so it may not contain separators or dots
// that could walk out of it.
var projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ErrBadProject reports a project name that is not usable as a directory.
var ErrBadProject = errors.New("invalid project name")

// ErrNoProject reports that a project has no repository in the hub.
var ErrNoProject = errors.New("project not found")

// Credentials authenticate the harness to a project's upstream remote. The
// zero value is a public remote.
type Credentials struct {
	// Username is the remote user. For a token-only remote it is usually a
	// placeholder such as "x-access-token".
	Username string
	// Password is the token or password. It reaches git through the
	// environment of the one command that needs it and is never written to
	// disk or put in an argument.
	Password string
}

// Hub owns the directory of bare repositories.
type Hub struct {
	root string
	log  *slog.Logger

	// mu guards the grants workspaces use to reach Handler.
	mu     sync.RWMutex
	grants map[string]grant
}

// grant is one workspace's access to the hub: a token, and the single project
// it may use that token on.
type grant struct {
	project string
	token   string
}

// New builds a Hub whose repositories live under root, creating the directory
// if it does not exist.
func New(root string, log *slog.Logger) (*Hub, error) {
	if root == "" {
		return nil, errors.New("build hub: root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve hub root %s: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create hub root %s: %w", abs, err)
	}
	return &Hub{root: abs, log: log, grants: make(map[string]grant)}, nil
}

// Root is the directory holding the bare repositories.
func (h *Hub) Root() string { return h.root }

// Path is the bare repository directory of a project, whether or not it
// exists.
func (h *Hub) Path(project string) (string, error) {
	if !projectName.MatchString(project) {
		return "", fmt.Errorf("%w: %q", ErrBadProject, project)
	}
	return filepath.Join(h.root, project+".git"), nil
}

// Init creates the project's bare repository if it is missing and returns its
// path. It is safe to call on every startup.
func (h *Hub) Init(ctx context.Context, project string) (string, error) {
	path, err := h.Path(project)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err == nil {
		return path, nil
	}
	if _, err := h.git(ctx, "", nil, "init", "--bare", "--initial-branch=main", path); err != nil {
		return "", err
	}
	// Smart HTTP refuses pushes unless the repository opts in.
	if _, err := h.git(ctx, path, nil, "config", "http.receivepack", "true"); err != nil {
		return "", err
	}
	h.log.Info("hub project initialised", "project", project)
	return path, nil
}

// List names every project in the hub.
func (h *Hub) List() ([]string, error) {
	entries, err := os.ReadDir(h.root)
	if err != nil {
		return nil, fmt.Errorf("read hub root %s: %w", h.root, err)
	}
	var out []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".git"); e.IsDir() && ok {
			out = append(out, name)
		}
	}
	return out, nil
}

// Mirror fetches every branch and tag of remoteURL into the project's
// repository, creating the repository if needed.
func (h *Hub) Mirror(ctx context.Context, project, remoteURL string, creds Credentials) error {
	path, err := h.Init(ctx, project)
	if err != nil {
		return err
	}
	if _, err := h.git(ctx, path, &creds, "fetch", "--prune", "--tags", remoteURL,
		"+refs/heads/*:refs/heads/*"); err != nil {
		return err
	}
	h.log.Info("hub project mirrored", "project", project)
	return nil
}

// Push sends refspec from the project's repository to remoteURL, for example
// "refs/heads/work:refs/heads/work".
func (h *Hub) Push(ctx context.Context, project, remoteURL, refspec string, creds Credentials) error {
	path, err := h.Path(project)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%w: %s", ErrNoProject, project)
	}
	if _, err := h.git(ctx, path, &creds, "push", remoteURL, refspec); err != nil {
		return err
	}
	h.log.Info("hub project pushed", "project", project, "refspec", refspec)
	return nil
}

// Grant lets a workspace reach one project through Handler with the given
// token, replacing any grant it held before. A workspace never has access to
// more than the single project it was created for.
func (h *Hub) Grant(workspaceID, project, token string) error {
	if !projectName.MatchString(project) {
		return fmt.Errorf("%w: %q", ErrBadProject, project)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.grants[workspaceID] = grant{project: project, token: token}
	return nil
}

// Revoke removes a workspace's access to Handler.
func (h *Hub) Revoke(workspaceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.grants, workspaceID)
}

// git runs one git command in dir. When creds is set, git is told to answer
// credential prompts from the environment rather than from a terminal or a
// stored credential file.
func (h *Hub) git(ctx context.Context, dir string, creds *Credentials, args ...string) (string, error) {
	full := args
	if creds != nil {
		if (creds.Username == "") != (creds.Password == "") {
			return "", errors.New("authenticate to remote: username and password must both be set")
		}
		full = append([]string{
			"-c", "credential.helper=",
			"-c", "credential.helper=" + credentialHelper,
		}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if creds != nil {
		cmd.Env = append(cmd.Env,
			credUserEnv+"="+creds.Username,
			credPasswordEnv+"="+creds.Password,
		)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
