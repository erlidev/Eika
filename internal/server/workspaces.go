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
	ID                string `json:"id"`
	ProjectID         string `json:"project_id"`
	Name              string `json:"name"`
	Branch            string `json:"branch"`
	BaseCommit        string `json:"base_commit,omitempty"`
	Image             string `json:"image"`
	State             string `json:"state"`
	ContainerID       string `json:"container_id,omitempty"`
	ParentWorkspaceID string `json:"parent_workspace_id,omitempty"`
	// WorktreeOf names the workspace whose container this one is a git
	// worktree in. Such a workspace starts, stops, and is confined with its
	// holder, whose sandbox it shares; its own sandbox is empty.
	WorktreeOf string `json:"worktree_of,omitempty"`
	// Sandbox is what the container may consume, reach, and expose.
	Sandbox sandboxBody `json:"sandbox"`
	// Pinned workspaces are listed before the others.
	Pinned bool `json:"pinned"`
	// Archived workspaces are set aside under a heading of their own; the
	// container is left as it is.
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	// Sandbox is the new workspace's limits and network. Left out, it gets
	// the settings' defaults.
	Sandbox *sandboxBody `json:"sandbox"`
}

// updateWorkspaceRequest is the body of PATCH /api/workspaces/{id}. An
// absent field is left alone. The branch and the container do not change.
type updateWorkspaceRequest struct {
	Name     *string `json:"name"`
	Pinned   *bool   `json:"pinned"`
	Archived *bool   `json:"archived"`
}

// workspacesResponse is the body of GET /api/workspaces.
type workspacesResponse struct {
	Workspaces []workspaceBody `json:"workspaces"`
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
	name, err := checkName("name", req.Name)
	if err != nil {
		return store.Workspace{}, err
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

	sandbox, err := s.newSandbox(ctx, req.Sandbox)
	if err != nil {
		return store.Workspace{}, err
	}

	image := strings.TrimSpace(req.Image)
	if image == "" && req.BuildContext == "" {
		image = s.sandboxImage(ctx)
	}
	spec := workspace.Spec{
		ID:           store.NewID(),
		Image:        image,
		BuildContext: req.BuildContext,
		Dockerfile:   req.Dockerfile,
		HostPath:     project.HostPath,
		Confinement:  confinement(sandbox),
	}
	host, err := s.deps.Workspaces.Create(ctx, spec)
	if err != nil {
		return store.Workspace{}, err
	}
	if err := s.deps.Workspaces.Start(ctx, &host); err != nil {
		s.discard(ctx, host)
		return store.Workspace{}, err
	}

	// A project is bind-mounted and already holds the user's checkout. The
	// workspace records the commit it starts from, which a worktree
	// subagent's branch is cut from.
	base := s.head(ctx, host)

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
		Sandbox:           sandbox,
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

// handleUpdateWorkspace renames, pins, or archives a workspace. The state
// event it publishes, with the state unchanged, is how the sidebars that
// show the workspace learn of it.
func (s *Server) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[updateWorkspaceRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	changes := store.WorkspaceChanges{Pinned: req.Pinned, Archived: req.Archived}
	if req.Name != nil {
		name, err := checkName("name", *req.Name)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		changes.Name = &name
	}
	ws, err := s.deps.Store.UpdateWorkspace(r.Context(), r.PathValue("id"), changes)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.workspaceState(r.Context(), ws.ID, ws.ProjectID, ws.State)
	s.log.Info("workspace updated", "workspace_id", ws.ID)
	writeJSON(w, s.log, http.StatusOK, asWorkspace(ws))
}

// handleStartWorkspace starts a stopped workspace's container again, held
// to the sandbox its row records: a change that reached the container only
// in part before it stopped is completed here.
func (s *Server) handleStartWorkspace(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, func(ctx context.Context, ws store.Workspace, host *workspace.Workspace) error {
		if err := s.deps.Workspaces.Confine(ctx, host, confinement(ws.Sandbox)); err != nil {
			return err
		}
		if err := s.deps.Workspaces.Start(ctx, host); err != nil {
			return err
		}
		// A workspace started by hand gets the whole idle timeout, however
		// long it sat unused before it stopped.
		s.idle.used(ws.ID, time.Now())
		return nil
	})
}

// handleStopWorkspace stops a workspace's container, keeping its files.
func (s *Server) handleStopWorkspace(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, s.stopWorkspace)
}

// stopWorkspace stops the runs in a workspace and then its container.
func (s *Server) stopWorkspace(ctx context.Context, ws store.Workspace, host *workspace.Workspace) error {
	s.stopRunsIn(ctx, ws)
	return s.deps.Workspaces.Stop(ctx, host)
}

// transition answers a request for one lifecycle operation on a workspace.
// A worktree workspace has no container to start or stop.
func (s *Server) transition(w http.ResponseWriter, r *http.Request, op func(context.Context, store.Workspace, *workspace.Workspace) error) {
	ws, err := s.deps.Store.Workspace(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if ws.WorktreeOf != "" {
		s.fail(w, r, conflictf("workspace %s is a worktree in workspace %s: start or stop that one", ws.ID, ws.WorktreeOf))
		return
	}
	if ws, err = s.changeState(r.Context(), ws, op); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asWorkspace(ws))
}

// changeState runs one lifecycle operation on a workspace that holds its own
// container and records the state it reached, for the worktrees it holds as
// well: they live in its container.
func (s *Server) changeState(ctx context.Context, ws store.Workspace, op func(context.Context, store.Workspace, *workspace.Workspace) error) (store.Workspace, error) {
	host, err := s.deps.Workspaces.Inspect(ctx, ws.ID)
	if err != nil {
		return store.Workspace{}, err
	}
	if err := op(ctx, ws, &host); err != nil {
		return store.Workspace{}, err
	}
	worktrees, err := s.worktreesOf(ctx, ws)
	if err != nil {
		return store.Workspace{}, err
	}
	for _, held := range append([]store.Workspace{ws}, worktrees...) {
		if err := s.deps.Store.SetWorkspaceState(ctx, held.ID, string(host.State), host.ContainerID); err != nil {
			return store.Workspace{}, err
		}
		s.workspaceState(ctx, held.ID, held.ProjectID, string(host.State))
	}
	ws.State = string(host.State)
	return ws, nil
}

// worktreesOf lists the worktree workspaces a workspace holds.
func (s *Server) worktreesOf(ctx context.Context, holder store.Workspace) ([]store.Workspace, error) {
	if holder.WorktreeOf != "" {
		return nil, nil
	}
	all, err := s.deps.Store.Workspaces(ctx, holder.ProjectID)
	if err != nil {
		return nil, err
	}
	var held []store.Workspace
	for _, ws := range all {
		if ws.WorktreeOf == holder.ID {
			held = append(held, ws)
		}
	}
	return held, nil
}

// locate finds where a workspace's files are: its own container, or, for a
// worktree workspace, its worktree in its holder's container.
func (s *Server) locate(ctx context.Context, ws store.Workspace) (workspace.Workspace, error) {
	return workspace.Locate(ctx, s.deps.Workspaces, ws.ID, ws.WorktreeOf)
}

// notRunning is the conflict a workspace whose container is not running
// answers with. A worktree workspace's container is its holder's, which is
// the one to start.
func notRunning(ws store.Workspace, state workspace.State) error {
	if ws.WorktreeOf != "" {
		return conflictf("workspace %s is %s, not running: start workspace %s, which holds it", ws.ID, state, ws.WorktreeOf)
	}
	return conflictf("workspace %s is %s, not running", ws.ID, state)
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
// deletes its rows, with the worktree workspaces it holds, whose files are in
// its volume. A container that is already gone is not an error: the row is
// what the user asked to be rid of.
func (s *Server) destroyWorkspace(ctx context.Context, ws store.Workspace) error {
	if ws.WorktreeOf != "" {
		return s.destroyWorktree(ctx, ws)
	}
	worktrees, err := s.worktreesOf(ctx, ws)
	if err != nil {
		return err
	}
	s.stopRunsIn(ctx, ws)
	host, err := s.deps.Workspaces.Inspect(ctx, ws.ID)
	switch {
	case err == nil:
		if err := s.deps.Workspaces.Destroy(ctx, &host); err != nil {
			return err
		}
	case errors.Is(err, workspace.ErrNoWorkspace):
		s.log.Warn("workspace container is already gone", "workspace_id", ws.ID, "error", err)
	default:
		return err
	}
	if err := s.deps.Store.DeleteWorkspace(ctx, ws.ID); err != nil {
		return err
	}
	s.forgetEgress(ws.ID)
	for _, held := range append(worktrees, ws) {
		s.workspaceState(ctx, held.ID, held.ProjectID, string(workspace.StateGone))
	}
	return nil
}

// destroyWorktree removes a worktree workspace's worktree, its files and all,
// and its rows. Its branch stays in the holder's repository and in the hub.
// A holder that is stopped keeps the files until it runs again, so it is
// refused rather than left with a directory no row accounts for.
func (s *Server) destroyWorktree(ctx context.Context, ws store.Workspace) error {
	host, err := s.locate(ctx, ws)
	switch {
	case errors.Is(err, workspace.ErrNoWorkspace):
		s.log.Warn("worktree's holder container is already gone", "workspace_id", ws.ID, "holder_id", ws.WorktreeOf)
	case err != nil:
		return err
	case host.State != workspace.StateRunning:
		return notRunning(ws, host.State)
	default:
		s.stopRunsIn(ctx, ws)
		host.Dir = ""
		ex, err := s.deps.Workspaces.Executor(host)
		if err != nil {
			return err
		}
		if err := workspace.RemoveWorktree(ctx, ex, ws.ID); err != nil {
			return err
		}
	}
	if err := s.deps.Store.DeleteWorkspace(ctx, ws.ID); err != nil {
		return err
	}
	s.workspaceState(ctx, ws.ID, ws.ProjectID, string(workspace.StateGone))
	return nil
}

// stopRunsIn aborts every run in a workspace and in the worktrees it holds,
// then ends the stdio MCP servers running there, before the container goes
// away under them. It detaches from the request for the same reason discard
// does: a run left going would keep writing through an executor that no
// longer has a container behind it.
func (s *Server) stopRunsIn(ctx context.Context, ws store.Workspace) {
	ctx, cancel := teardown(ctx)
	defer cancel()
	worktrees, err := s.worktreesOf(ctx, ws)
	if err != nil {
		s.log.Error("list worktrees of workspace", "workspace_id", ws.ID, "error", err)
	}
	for _, held := range append(worktrees, ws) {
		s.runs.stopSessionsOf(ctx, s.deps.Store, held.ID)
		if s.deps.MCP != nil {
			s.deps.MCP.StopWorkspace(held.ID)
		}
	}
}

// executorFor returns the executor of a running workspace, rooted where its
// files are.
func (s *Server) executorFor(ctx context.Context, id string) (executor.Executor, error) {
	ws, err := s.deps.Store.Workspace(ctx, id)
	if err != nil {
		return nil, err
	}
	host, err := s.locate(ctx, ws)
	if err != nil {
		return nil, err
	}
	if host.State != workspace.StateRunning {
		return nil, notRunning(ws, host.State)
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
		WorktreeOf:        ws.WorktreeOf,
		Sandbox:           asSandbox(ws.Sandbox),
		Pinned:            ws.Pinned,
		Archived:          ws.Archived,
		CreatedAt:         ws.CreatedAt,
		UpdatedAt:         ws.UpdatedAt,
	}
}
