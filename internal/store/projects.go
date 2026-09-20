package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ProjectKind is where a project's code lives.
type ProjectKind string

// The kinds of project Eika knows.
const (
	// ProjectRemote is a project mirrored from a git remote such as GitHub.
	ProjectRemote ProjectKind = "remote"
	// ProjectLocal is a project backed by a directory on the Docker host,
	// bind-mounted into its workspaces.
	ProjectLocal ProjectKind = "local"
)

// Project is a git repository Eika knows about.
type Project struct {
	ID   string
	Name string
	Kind ProjectKind
	// RemoteURL is the git remote a ProjectRemote mirrors, empty otherwise.
	RemoteURL string
	// RemoteUsername is the user the hub authenticates to the remote as. It
	// is empty for a public remote.
	RemoteUsername string
	// RemotePassword is the remote password or token as the harness sealed
	// it, nil for a public remote. The store never sees it in the clear.
	RemotePassword []byte
	// HostPath is the Docker host directory a ProjectLocal lives in, empty
	// otherwise.
	HostPath string
	// DefaultBranch is the branch a workspace starts from.
	DefaultBranch string
	CreatedAt     time.Time
}

// projectColumns is the column list every project query selects, in the order
// scanProject reads them.
const projectColumns = `id, name, kind, remote_url, remote_username, remote_password,
	host_path, default_branch, created_at`

// CreateProject inserts p and returns it with the fields the database
// assigned. An empty ID gets a fresh one; a duplicate name is ErrConflict.
func (s *Store) CreateProject(ctx context.Context, p Project) (Project, error) {
	if p.ID == "" {
		p.ID = NewID()
	}
	const q = `INSERT INTO projects (id, name, kind, remote_url, remote_username,
		remote_password, host_path, default_branch)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + projectColumns
	row := s.pool.QueryRow(ctx, q, p.ID, p.Name, p.Kind, p.RemoteURL,
		p.RemoteUsername, p.RemotePassword, p.HostPath, p.DefaultBranch)
	out, err := scanProject(row)
	if err != nil {
		return Project{}, wrap("create project "+p.Name, err)
	}
	return out, nil
}

// Project returns the project with the given id.
func (s *Store) Project(ctx context.Context, id string) (Project, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = $1`, id)
	p, err := scanProject(row)
	if err != nil {
		return Project{}, wrap("read project "+id, err)
	}
	return p, nil
}

// ProjectByName returns the project with the given name.
func (s *Store) ProjectByName(ctx context.Context, name string) (Project, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE name = $1`, name)
	p, err := scanProject(row)
	if err != nil {
		return Project{}, wrap("read project "+name, err)
	}
	return p, nil
}

// Projects returns every project, oldest first.
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY created_at, id`)
	if err != nil {
		return nil, wrap("list projects", err)
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, wrap("list projects", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list projects", err)
	}
	return out, nil
}

// UpdateProject writes p's remote credentials and default branch over the
// stored row and returns the row as it now stands. Where a project's code
// comes from does not change after it is created.
func (s *Store) UpdateProject(ctx context.Context, p Project) (Project, error) {
	const q = `UPDATE projects SET remote_username = $2, remote_password = $3, default_branch = $4
		WHERE id = $1
		RETURNING ` + projectColumns
	out, err := scanProject(s.pool.QueryRow(ctx, q, p.ID, p.RemoteUsername, p.RemotePassword, p.DefaultBranch))
	if err != nil {
		return Project{}, wrap("update project "+p.ID, err)
	}
	return out, nil
}

// DeleteProject removes a project and everything that hangs off it.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return wrap("delete project "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("delete project "+id, pgx.ErrNoRows)
	}
	return nil
}

// scanProject reads one project row.
func scanProject(row pgx.Row) (Project, error) {
	var p Project
	if err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.RemoteURL, &p.RemoteUsername,
		&p.RemotePassword, &p.HostPath, &p.DefaultBranch, &p.CreatedAt); err != nil {
		return Project{}, err
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}
