package store

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Sandbox is what the container may consume, reach, and expose.
	Sandbox   WorkspaceSandbox
	CreatedAt time.Time
	UpdatedAt time.Time
}

// WorkspaceSandbox is what a workspace's container may consume, reach, and
// expose. The zero value is a workspace with no limits, open egress, and no
// ports, which is what every workspace was before the column existed.
type WorkspaceSandbox struct {
	// CPUs is the number of cores the container may use; zero is no limit.
	CPUs float64 `json:"cpus,omitempty"`
	// MemoryMB is the memory limit in MiB; zero is no limit.
	MemoryMB int64 `json:"memory_mb,omitempty"`
	// PIDs is the process and thread limit; zero is no limit.
	PIDs int64 `json:"pids,omitempty"`
	// Egress is the egress mode, empty for open.
	Egress string `json:"egress,omitempty"`
	// Allow is the egress allowlist, used when Egress is allowlist.
	Allow []string `json:"allow,omitempty"`
	// Ports are the container's ports the harness forwards previews to.
	Ports []WorkspacePort `json:"ports,omitempty"`
}

// WorkspacePort is one port of a workspace that the harness forwards to.
type WorkspacePort struct {
	Port int `json:"port"`
	// Label says what listens there, such as "vite".
	Label string `json:"label,omitempty"`
}

// workspaceColumns is the column list every workspace query selects, in the
// order scanWorkspace reads them.
const workspaceColumns = `id, project_id, name, branch, base_commit, image, state, container_id,
	parent_workspace_id, sandbox, created_at, updated_at`

// CreateWorkspace inserts w and returns it with the fields the database
// assigned. An empty ID gets a fresh one.
func (s *Store) CreateWorkspace(ctx context.Context, w Workspace) (Workspace, error) {
	if w.ID == "" {
		w.ID = NewID()
	}
	sandbox, err := json.Marshal(w.Sandbox)
	if err != nil {
		return Workspace{}, fmt.Errorf("encode sandbox of workspace %s: %w", w.ID, err)
	}
	const q = `INSERT INTO workspaces
		(id, project_id, name, branch, base_commit, image, state, container_id, parent_workspace_id, sandbox)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + workspaceColumns
	row := s.pool.QueryRow(ctx, q, w.ID, w.ProjectID, w.Name, w.Branch, w.BaseCommit, w.Image,
		w.State, w.ContainerID, nullable(w.ParentWorkspaceID), sandbox)
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
	// One query per case: a WHERE that is true for every row when the filter
	// is empty cannot use the project index.
	const (
		all = `SELECT ` + workspaceColumns + ` FROM workspaces ORDER BY created_at, id`
		one = `SELECT ` + workspaceColumns + ` FROM workspaces
			WHERE project_id = $1 ORDER BY created_at, id`
	)
	var (
		rows pgx.Rows
		err  error
	)
	if projectID == "" {
		rows, err = s.pool.Query(ctx, all)
	} else {
		rows, err = s.pool.Query(ctx, one, projectID)
	}
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

// SetWorkspaceSandbox records what a workspace's container may consume,
// reach, and expose, and returns the workspace as it now stands.
func (s *Store) SetWorkspaceSandbox(ctx context.Context, id string, sandbox WorkspaceSandbox) (Workspace, error) {
	encoded, err := json.Marshal(sandbox)
	if err != nil {
		return Workspace{}, fmt.Errorf("encode sandbox of workspace %s: %w", id, err)
	}
	row := s.pool.QueryRow(ctx, `UPDATE workspaces SET sandbox = $2, updated_at = now()
		WHERE id = $1 RETURNING `+workspaceColumns, id, encoded)
	w, err := scanWorkspace(row)
	if err != nil {
		return Workspace{}, wrap("set sandbox of workspace "+id, err)
	}
	return w, nil
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
		w       Workspace
		parent  *string
		sandbox []byte
	)
	err := row.Scan(&w.ID, &w.ProjectID, &w.Name, &w.Branch, &w.BaseCommit, &w.Image,
		&w.State, &w.ContainerID, &parent, &sandbox, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Workspace{}, err
	}
	if err := json.Unmarshal(sandbox, &w.Sandbox); err != nil {
		return Workspace{}, fmt.Errorf("decode sandbox of workspace %s: %w", w.ID, err)
	}
	w.ParentWorkspaceID = text(parent)
	w.CreatedAt = w.CreatedAt.UTC()
	w.UpdatedAt = w.UpdatedAt.UTC()
	return w, nil
}
