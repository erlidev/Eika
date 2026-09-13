package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/workspace"
)

// gitTimeout bounds one git command the API runs inside a workspace.
const gitTimeout = 30 * time.Second

// teardownTimeout bounds the work that has to finish even though the request
// that asked for it is gone: removing a half-created workspace, and stopping
// the runs in a workspace that is about to go away. Both leave a container,
// a volume, or a goroutine behind if they are cut short.
const teardownTimeout = 2 * time.Minute

// workspaceBody is one workspace on the wire.
type workspaceBody struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"project_id"`
	Name              string    `json:"name"`
	Branch            string    `json:"branch"`
	BaseCommit        string    `json:"base_commit,omitempty"`
	Image             string    `json:"image"`
	State             string    `json:"state"`
	ContainerID       string    `json:"container_id,omitempty"`
	ParentWorkspaceID string    `json:"parent_workspace_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// createWorkspaceRequest is the body of POST /api/workspaces. Image and
// BuildContext are alternatives: a build context is a directory on the Docker
// host holding Dockerfile and whatever it needs.
type createWorkspaceRequest struct {
	ProjectID         string `json:"project_id"`
	Name              string `json:"name"`
	Branch            string `json:"branch"`
	Image             string `json:"image"`
	BuildContext      string `json:"build_context"`
	Dockerfile        string `json:"dockerfile"`
	ParentWorkspaceID string `json:"parent_workspace_id"`
}

// workspacesResponse is the body of GET /api/workspaces.
type workspacesResponse struct {
	Workspaces []workspaceBody `json:"workspaces"`
}

// diffResponse is the body of GET /api/workspaces/{id}/diff: the workspace's
// changes against the commit it started from.
type diffResponse struct {
	WorkspaceID string `json:"workspace_id"`
	BaseCommit  string `json:"base_commit,omitempty"`
	// Diff is the output of git diff against the base commit.
	Diff string `json:"diff"`
	// Status is the output of git status --porcelain, which lists untracked
	// files the diff does not show.
	Status string `json:"status"`
}

// handleListWorkspaces lists the workspaces, of one project when the query
// names one.
func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	workspaces, err := s.deps.Store.Workspaces(r.Context(), r.URL.Query().Get("project_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]workspaceBody, 0, len(workspaces))
	for _, ws := range workspaces {
		out = append(out, asWorkspace(ws))
	}
	writeJSON(w, s.log, http.StatusOK, workspacesResponse{Workspaces: out})
}

// handleCreateWorkspace creates the container, starts it, puts the project's
// code in it, and records the result.
func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createWorkspaceRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ws, err := s.createWorkspace(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.workspaceState(r.Context(), ws.ID, ws.ProjectID, ws.State)
	s.log.Info("workspace created", "workspace_id", ws.ID, "project_id", ws.ProjectID, "branch", ws.Branch)
	writeJSON(w, s.log, http.StatusCreated, asWorkspace(ws))
}

// createWorkspace runs the whole creation sequence. Everything the host
// created is destroyed again when a later step fails, so a failed request
// leaves no container behind and writes no row.
func (s *Server) createWorkspace(ctx context.Context, req createWorkspaceRequest) (store.Workspace, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return store.Workspace{}, invalidf("name is required")
	}
	project, err := s.deps.Store.Project(ctx, req.ProjectID)
	if err != nil {
		return store.Workspace{}, err
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = project.DefaultBranch
	}
	if req.ParentWorkspaceID != "" {
		if _, err := s.deps.Store.Workspace(ctx, req.ParentWorkspaceID); err != nil {
			return store.Workspace{}, err
		}
	}

	spec := workspace.Spec{
		ID:           store.NewID(),
		Image:        req.Image,
		BuildContext: req.BuildContext,
		Dockerfile:   req.Dockerfile,
		Project:      project.Name,
		HostPath:     project.HostPath,
	}
	host, err := s.deps.Workspaces.Create(ctx, spec)
	if err != nil {
		return store.Workspace{}, err
	}
	if err := s.deps.Workspaces.Start(ctx, &host); err != nil {
		s.discard(ctx, host)
		return store.Workspace{}, err
	}

	// A local project is bind-mounted and already holds the user's checkout,
	// so only a hub-backed workspace clones. Either way the workspace records
	// the commit it starts from, which is what a diff and a fork need.
	base := ""
	if project.Kind == store.ProjectLocal {
		base = s.head(ctx, host)
	} else if base, err = s.deps.Workspaces.Clone(ctx, host, project.Name, branch); err != nil {
		s.discard(ctx, host)
		return store.Workspace{}, err
	}

	ws, err := s.deps.Store.CreateWorkspace(ctx, store.Workspace{
		ID:                host.ID,
		ProjectID:         project.ID,
		Name:              name,
		Branch:            branch,
		BaseCommit:        base,
		Image:             host.Image,
		State:             string(host.State),
		ContainerID:       host.ContainerID,
		ParentWorkspaceID: req.ParentWorkspaceID,
	})
	if err != nil {
		s.discard(ctx, host)
		return store.Workspace{}, err
	}
	return ws, nil
}

// handleWorkspace returns one workspace.
func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asWorkspace(ws))
}

// handleStartWorkspace starts a stopped workspace's container again.
func (s *Server) handleStartWorkspace(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, func(ctx context.Context, host *workspace.Workspace) error {
		return s.deps.Workspaces.Start(ctx, host)
	})
}

// handleStopWorkspace stops a workspace's container, keeping its files.
func (s *Server) handleStopWorkspace(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, func(ctx context.Context, host *workspace.Workspace) error {
		s.stopRunsIn(ctx, host.ID)
		return s.deps.Workspaces.Stop(ctx, host)
	})
}

// transition runs one lifecycle operation on a workspace and records the
// state it reached.
func (s *Server) transition(w http.ResponseWriter, r *http.Request, op func(context.Context, *workspace.Workspace) error) {
	ws, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	host, err := s.deps.Workspaces.Inspect(r.Context(), ws.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := op(r.Context(), &host); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.deps.Store.SetWorkspaceState(r.Context(), ws.ID, string(host.State), host.ContainerID); err != nil {
		s.fail(w, r, err)
		return
	}
	ws.State = string(host.State)
	s.workspaceState(r.Context(), ws.ID, ws.ProjectID, ws.State)
	writeJSON(w, s.log, http.StatusOK, asWorkspace(ws))
}

// handleDeleteWorkspace destroys a workspace's container and volume and
// removes its rows, its sessions with them.
func (s *Server) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.destroyWorkspace(r.Context(), ws); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("workspace deleted", "workspace_id", ws.ID)
	w.WriteHeader(http.StatusNoContent)
}

// destroyWorkspace stops the runs in a workspace, removes its container, and
// deletes its rows. A container that is already gone is not an error: the row
// is what the user asked to be rid of.
func (s *Server) destroyWorkspace(ctx context.Context, ws store.Workspace) error {
	s.stopRunsIn(ctx, ws.ID)
	host, err := s.deps.Workspaces.Inspect(ctx, ws.ID)
	if err == nil {
		if err := s.deps.Workspaces.Destroy(ctx, &host); err != nil {
			return err
		}
	} else if errors.Is(err, workspace.ErrNoWorkspace) {
		s.log.Warn("workspace container is already gone", "workspace_id", ws.ID, "error", err)
	} else {
		return err
	}
	if err := s.deps.Store.DeleteWorkspace(ctx, ws.ID); err != nil {
		return err
	}
	s.workspaceState(ctx, ws.ID, ws.ProjectID, string(workspace.StateGone))
	return nil
}

// stopRunsIn aborts every run in a workspace before the workspace goes away
// under it. It detaches from the request for the same reason discard does: a
// run left going would keep writing through an executor that no longer has a
// container behind it.
func (s *Server) stopRunsIn(ctx context.Context, workspaceID string) {
	ctx, cancel := teardown(ctx)
	defer cancel()
	s.runs.stopSessionsOf(ctx, s.deps.Store, workspaceID)
}

// handleWorkspaceDiff reports what the workspace changed since it was
// created, as git sees it.
func (s *Server) handleWorkspaceDiff(w http.ResponseWriter, r *http.Request) {
	ws, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ex, err := s.executorFor(r.Context(), ws.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	args := []string{"diff"}
	if ws.BaseCommit != "" {
		args = append(args, ws.BaseCommit)
	}
	diff, err := gitOutput(r.Context(), ex, args...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status, err := gitOutput(r.Context(), ex, "status", "--porcelain")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, diffResponse{
		WorkspaceID: ws.ID,
		BaseCommit:  ws.BaseCommit,
		Diff:        diff,
		Status:      status,
	})
}

// executorFor returns the executor of a running workspace.
func (s *Server) executorFor(ctx context.Context, id string) (executor.Executor, error) {
	host, err := s.deps.Workspaces.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if host.State != workspace.StateRunning {
		return nil, conflictf("workspace %s is %s, not running", id, host.State)
	}
	return s.deps.Workspaces.Executor(host)
}

// discard removes a workspace the harness created but could not finish. It
// runs on a context of its own: a client that gave up on the request is the
// most likely reason the creation failed, and a cancelled cleanup would leave
// the container and its volume behind for good.
func (s *Server) discard(ctx context.Context, host workspace.Workspace) {
	ctx, cancel := teardown(ctx)
	defer cancel()
	if err := s.deps.Workspaces.Destroy(ctx, &host); err != nil {
		s.log.Error("discard half-created workspace", "workspace_id", host.ID, "error", err)
	}
}

// discardRecorded removes a workspace that already has a row, which is what a
// request that failed after creating one leaves behind. Deleting the row
// takes the sessions in it with it.
func (s *Server) discardRecorded(ctx context.Context, id string) {
	ctx, cancel := teardown(ctx)
	defer cancel()
	host, err := s.deps.Workspaces.Inspect(ctx, id)
	if err == nil {
		if err := s.deps.Workspaces.Destroy(ctx, &host); err != nil {
			s.log.Error("discard half-created workspace", "workspace_id", id, "error", err)
		}
	}
	if err := s.deps.Store.DeleteWorkspace(ctx, id); err != nil {
		s.log.Error("remove half-created workspace row", "workspace_id", id, "error", err)
	}
}

// teardown detaches a context from the request that carried it, keeping a
// bound of its own so that nothing runs forever.
func teardown(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), teardownTimeout)
}

// head reads the workspace's current HEAD commit, reporting none when the
// workspace holds no repository yet.
func (s *Server) head(ctx context.Context, host workspace.Workspace) string {
	ex, err := s.deps.Workspaces.Executor(host)
	if err != nil {
		return ""
	}
	out, err := gitOutput(ctx, ex, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitOutput runs one git command in a workspace and returns its standard
// output. A non-zero exit carries git's own message, which is what the user
// needs to see.
func gitOutput(ctx context.Context, ex executor.Executor, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	res, err := ex.Exec(ctx, executor.ExecSpec{
		Command: "git",
		Args:    args,
		Timeout: gitTimeout,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", invalidf("git %s: exit %d: %s", args[0], res.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// asWorkspace renders a stored workspace on the wire.
func asWorkspace(ws store.Workspace) workspaceBody {
	return workspaceBody{
		ID:                ws.ID,
		ProjectID:         ws.ProjectID,
		Name:              ws.Name,
		Branch:            ws.Branch,
		BaseCommit:        ws.BaseCommit,
		Image:             ws.Image,
		State:             ws.State,
		ContainerID:       ws.ContainerID,
		ParentWorkspaceID: ws.ParentWorkspaceID,
		CreatedAt:         ws.CreatedAt,
		UpdatedAt:         ws.UpdatedAt,
	}
}
