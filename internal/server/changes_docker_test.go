//go:build docker

package server_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/workspace"
	"github.com/erlidev/eika/internal/workspace/hub"
)

// commitWire is the body of a commit.
type commitWire struct {
	Commit string `json:"commit"`
}

// pushWire is the body of a push.
type pushWire struct {
	Branch         string `json:"branch"`
	Commit         string `json:"commit"`
	UpstreamPushed bool   `json:"upstream_pushed"`
}

// pushed returns the pushes recorded so far.
func (h *fakeHub) pushed() []hubPush {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hubPush(nil), h.pushes...)
}

// gitIn runs git in a directory of the test's own and returns its output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCommitRoute(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	commitPath := "/api/workspaces/" + ws.ID + "/commit"

	t.Run("commits every change", func(t *testing.T) {
		sub := a.Server.Bus().Subscribe(event.WorkspaceTopic(ws.ID))
		defer sub.Close()
		write(t, dir, "hello.txt", "changed\n")
		write(t, dir, "new.txt", "new\n")
		got := decodeBody[commitWire](t, request(t, a.Server, "POST", commitPath,
			map[string]any{"message": "change everything"}), 200)
		if head := gitIn(t, dir, "rev-parse", "HEAD"); got.Commit != head {
			t.Errorf("commit = %q, want HEAD %q", got.Commit, head)
		}
		if subject := gitIn(t, dir, "log", "-1", "--format=%s"); subject != "change everything" {
			t.Errorf("subject = %q", subject)
		}
		if status := gitIn(t, dir, "status", "--porcelain"); status != "" {
			t.Errorf("status after commit = %q, want clean", status)
		}
		// The repository's own identity is kept.
		if author := gitIn(t, dir, "log", "-1", "--format=%ae"); author != "eika@example.invalid" {
			t.Errorf("author = %q, want the repository's identity", author)
		}
		if e := nextWorkspaceEvent(t, sub); e.WorkspaceID != ws.ID {
			t.Errorf("event = %+v, want this workspace", e)
		}
	})

	t.Run("commits only the paths named", func(t *testing.T) {
		write(t, dir, "hello.txt", "again\n")
		write(t, dir, "other.txt", "left alone\n")
		if err := os.Remove(filepath.Join(dir, "new.txt")); err != nil {
			t.Fatalf("remove: %v", err)
		}
		decodeBody[commitWire](t, request(t, a.Server, "POST", commitPath,
			map[string]any{"message": "some", "paths": []string{"hello.txt", "new.txt"}}), 200)
		files := gitIn(t, dir, "show", "--name-status", "--format=", "HEAD")
		if files != "M\thello.txt\nD\tnew.txt" {
			t.Errorf("committed = %q, want hello.txt modified and new.txt deleted", files)
		}
		if status := gitIn(t, dir, "status", "--porcelain"); status != "?? other.txt" {
			t.Errorf("status = %q, want other.txt left untracked", status)
		}
	})

	t.Run("commits as Eika when the repository names nobody", func(t *testing.T) {
		t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		gitIn(t, dir, "config", "--unset", "user.email")
		gitIn(t, dir, "config", "--unset", "user.name")
		decodeBody[commitWire](t, request(t, a.Server, "POST", commitPath,
			map[string]any{"message": "anonymous", "paths": []string{"other.txt"}}), 200)
		if author := gitIn(t, dir, "log", "-1", "--format=%ae"); author != workspace.GitUserEmail {
			t.Errorf("author = %q, want %q", author, workspace.GitUserEmail)
		}
	})

	t.Run("refuses what it cannot commit", func(t *testing.T) {
		cases := []struct {
			name   string
			body   map[string]any
			status int
		}{
			{"a clean tree", map[string]any{"message": "nothing"}, 409},
			{"an empty message", map[string]any{"message": "  "}, 400},
			{"a path outside the root", map[string]any{"message": "x", "paths": []string{"../x"}}, 403},
			{"an empty path", map[string]any{"message": "x", "paths": []string{""}}, 400},
			{"an unknown field", map[string]any{"message": "x", "amend": true}, 400},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				rec := request(t, a.Server, "POST", commitPath, c.body)
				if rec.Code != c.status {
					t.Errorf("status = %d, want %d: %s", rec.Code, c.status, rec.Body.String())
				}
			})
		}
		code, message := errorOf(t, request(t, a.Server, "POST", commitPath, map[string]any{"message": "nothing"}))
		if code != "conflict" || message != "nothing to commit" {
			t.Errorf("clean tree error = %s %q", code, message)
		}
	})
}

func TestPushRoute(t *testing.T) {
	a := newAPI(t)

	t.Run("pushes a local project's workspace to the hub", func(t *testing.T) {
		project, dir := a.newProject(t, "local")
		initRepo(t, dir)
		ws := a.newWorkspace(t, project.ID)
		write(t, dir, "hello.txt", "pushed\n")
		decodeBody[commitWire](t, request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/commit",
			map[string]any{"message": "to push"}), 200)

		got := decodeBody[pushWire](t, request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/push",
			map[string]any{}), 200)
		if got.Branch != "main" || got.Commit != gitIn(t, dir, "rev-parse", "HEAD") || got.UpstreamPushed {
			t.Errorf("push = %+v", got)
		}
		content, err := a.host.hubShow(t.Context(), "local", "main", "hello.txt")
		if err != nil || content != "pushed" {
			t.Errorf("hub has %q, %v; want the pushed file", content, err)
		}

		// A local project has no remote beyond the hub.
		rec := request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/push", map[string]any{"upstream": true})
		if rec.Code != 400 {
			t.Errorf("upstream push of a local project = %d, want 400", rec.Code)
		}
	})

	t.Run("pushes on to the remote with the project's credentials", func(t *testing.T) {
		project := decodeBody[projectWire](t, request(t, a.Server, "POST", "/api/projects", map[string]any{
			"name": "remote", "kind": "remote", "remote_url": "https://example.invalid/r.git",
			"remote_username": "me", "remote_password": "s3cret-token",
		}), 201)
		ws := a.newWorkspace(t, project.ID)
		dir, err := a.host.dirOf(ws.ID)
		if err != nil {
			t.Fatalf("workspace dir: %v", err)
		}
		initRepo(t, dir)

		got := decodeBody[pushWire](t, request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/push",
			map[string]any{"upstream": true}), 200)
		if !got.UpstreamPushed || got.Branch != "main" {
			t.Errorf("push = %+v, want main pushed upstream", got)
		}
		pushes := a.hub.pushed()
		want := hubPush{
			project: "remote", remoteURL: "https://example.invalid/r.git",
			refspec: "refs/heads/main:refs/heads/main",
			creds:   hub.Credentials{Username: "me", Password: "s3cret-token"},
		}
		if len(pushes) != 1 || pushes[0] != want {
			t.Errorf("hub pushes = %+v, want %+v", pushes, want)
		}

		// A refusal is reported without the password git may echo.
		a.hub.pushErr = fmt.Errorf("remote rejected s3cret-token")
		rec := request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/push", map[string]any{"upstream": true})
		if rec.Code != 400 || strings.Contains(rec.Body.String(), "s3cret-token") {
			t.Errorf("failed upstream push = %d %s, want 400 without the password", rec.Code, rec.Body.String())
		}
	})

	t.Run("needs a running workspace", func(t *testing.T) {
		project, dir := a.newProject(t, "stopped")
		initRepo(t, dir)
		ws := a.newWorkspace(t, project.ID)
		if rec := request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/stop", nil); rec.Code != 200 {
			t.Fatalf("stop = %d", rec.Code)
		}
		if rec := request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/push", map[string]any{}); rec.Code != 409 {
			t.Errorf("push of a stopped workspace = %d, want 409", rec.Code)
		}
		if rec := request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/commit",
			map[string]any{"message": "x"}); rec.Code != 409 {
			t.Errorf("commit in a stopped workspace = %d, want 409", rec.Code)
		}
	})
}

func TestDiffNarrowsToAPath(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	write(t, dir, "hello.txt", "changed\n")
	write(t, dir, "new.txt", "new\n")

	diff := decodeBody[diffWire](t, request(t, a.Server, "GET", "/api/workspaces/"+ws.ID+"/diff?path=new.txt", nil), 200)
	if diff.Diff != "" || strings.TrimSpace(diff.Status) != "?? new.txt" {
		t.Errorf("diff of new.txt = %+v, want only its status", diff)
	}
	diff = decodeBody[diffWire](t, request(t, a.Server, "GET", "/api/workspaces/"+ws.ID+"/diff?path=hello.txt", nil), 200)
	if !strings.Contains(diff.Diff, "changed") || strings.Contains(diff.Status, "new.txt") {
		t.Errorf("diff of hello.txt = %+v, want its change alone", diff)
	}
	if rec := request(t, a.Server, "GET", "/api/workspaces/"+ws.ID+"/diff?path=../x", nil); rec.Code != 403 {
		t.Errorf("diff outside the root = %d, want 403", rec.Code)
	}
}
