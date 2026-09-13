package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Workspace is the persisted record of one sandbox container. The container
// itself is owned by internal/workspace; this row is what survives a harness
// restart and what the UI lists.
type Workspace struct {
	ID        string
	ProjectID string
	// Name is the human-readable name the UI shows.
	Name string
	// Branch is the git branch the workspace works on.
	Branch string
	// BaseCommit is the commit the workspace was cloned at.
	BaseCommit string
	// Image is the container image the workspace runs.
	Image string
	// State mirrors workspace.State: creating, running, stopped, or gone.
	State string
	// ContainerID is the Docker container id, empty before the container
	// exists.
	ContainerID string
	// ParentWorkspaceID is the workspace a subagent's workspace was cloned
	// from, empty for a workspace the user created.
	ParentWorkspaceID string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// workspaceColumns is the column list every workspace query selects, in the
// order scanWorkspace reads them.
const workspaceColumns = `id, project_id, name, branch, base_commit, image, state, container_id,
	parent_workspace_id, created_at, updated_at`

// CreateWorkspace inserts w and returns it with the fields the database
// assigned. An empty ID gets a fresh one.
func (s *Store) CreateWorkspace(ctx context.Context, w Workspace) (Workspace, error) {
	if w.ID == "" {
		w.ID = NewID()
	}
	const q = `INSERT INTO workspaces
		(id, project_id, name, branch, base_commit, image, state, container_id, parent_workspace_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + workspaceColumns
	row := s.pool.QueryRow(ctx, q, w.ID, w.ProjectID, w.Name, w.Branch, w.BaseCommit, w.Image,
		w.State, w.ContainerID, nullable(w.ParentWorkspaceID))
	out, err := scanWorkspace(row)
	if err != nil {
		return Workspace{}, wrap("create workspace "+w.ID, err)
	}
	return out, nil
}

// Workspace returns the workspace with the given id.
func (s *Store) Workspace(ctx context.Context, id string) (Workspace, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE id = $1`, id)
	w, err := scanWorkspace(row)
	if err != nil {
		return Workspace{}, wrap("read workspace "+id, err)
	}
	return w, nil
}

// Workspaces returns the workspaces of one project, oldest first. An empty
// projectID returns every workspace.
func (s *Store) Workspaces(ctx context.Context, projectID string) ([]Workspace, error) {
	const q = `SELECT ` + workspaceColumns + ` FROM workspaces
		WHERE $1 = '' OR project_id = $1
		ORDER BY created_at, id`
	rows, err := s.pool.Query(ctx, q, projectID)
	if err != nil {
		return nil, wrap("list workspaces", err)
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, wrap("list workspaces", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list workspaces", err)
	}
	return out, nil
}

// SetWorkspaceState records the state a workspace's container is in and the
// container it runs as. An empty containerID leaves the recorded one alone,
// because a stopped container keeps its id.
func (s *Store) SetWorkspaceState(ctx context.Context, id, state, containerID string) error {
	const q = `UPDATE workspaces
		SET state = $2,
		    container_id = CASE WHEN $3 = '' THEN container_id ELSE $3 END,
		    updated_at = now()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id, state, containerID)
	if err != nil {
		return wrap("set workspace state "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("set workspace state "+id, pgx.ErrNoRows)
	}
	return nil
}

// DeleteWorkspace removes a workspace and its sessions.
func (s *Store) DeleteWorkspace(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, id)
	if err != nil {
		return wrap("delete workspace "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("delete workspace "+id, pgx.ErrNoRows)
	}
	return nil
}

// scanWorkspace reads one workspace row.
func scanWorkspace(row pgx.Row) (Workspace, error) {
	var (
		w      Workspace
		parent *string
	)
	err := row.Scan(&w.ID, &w.ProjectID, &w.Name, &w.Branch, &w.BaseCommit, &w.Image,
		&w.State, &w.ContainerID, &parent, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Workspace{}, err
	}
	w.ParentWorkspaceID = text(parent)
	w.CreatedAt = w.CreatedAt.UTC()
	w.UpdatedAt = w.UpdatedAt.UTC()
	return w, nil
}
