package store

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// ProjectFields contains project metadata that agents can change.
type ProjectFields struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Directory   string `json:"directory"`
}

// Project is a durable area of work with a directory on the home computer.
type Project struct {
	ID   string
	Slug string
	ProjectFields
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrInvalidProject reports invalid project metadata.
var ErrInvalidProject = errors.New("invalid project")

func invalidProject(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidProject, fmt.Sprintf(format, args...))
}

var projectSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidateProjectSlug checks the stable project name used in paths and links.
func ValidateProjectSlug(slug string) error {
	if len(slug) > 80 || !projectSlug.MatchString(slug) {
		return invalidProject("slug must contain 1 to 80 lowercase letters, digits or single separating hyphens")
	}
	return nil
}

// Validate checks project text, status and the sandbox directory path.
func (p ProjectFields) Validate() error {
	for _, f := range []struct {
		name, value string
		limit       int
	}{
		{"name", p.Name, 200}, {"description", p.Description, 4096}, {"directory", p.Directory, 4096},
	} {
		if !utf8.ValidString(f.value) || strings.ContainsRune(f.value, 0) || len(f.value) > f.limit {
			return invalidProject("%s must be valid text of at most %d bytes without NUL", f.name, f.limit)
		}
	}
	if strings.TrimSpace(p.Name) == "" {
		return invalidProject("name must not be empty")
	}
	switch p.Status {
	case "active", "paused", "done", "archived":
	default:
		return invalidProject("status must be active, paused, done or archived")
	}
	if !path.IsAbs(p.Directory) || p.Directory == "/" || path.Clean(p.Directory) != p.Directory ||
		strings.Contains(p.Directory, `\`) || strings.ContainsFunc(p.Directory, unicode.IsControl) {
		return invalidProject("directory must be a clean absolute POSIX path other than / without control characters or backslashes")
	}
	return nil
}

const projectColumns = `id, slug, name, description, status, directory, created_at, updated_at`

// CreateProject inserts metadata with active status and the default directory when omitted.
func (s *Store) CreateProject(ctx context.Context, slug string, fields ProjectFields) (Project, error) {
	if err := ValidateProjectSlug(slug); err != nil {
		return Project{}, err
	}
	if fields.Status == "" {
		fields.Status = "active"
	}
	if fields.Directory == "" {
		fields.Directory = "/home/eika/projects/" + slug
	}
	if err := fields.Validate(); err != nil {
		return Project{}, err
	}
	p, err := scanProject(s.pool.QueryRow(ctx, `INSERT INTO projects (id, slug, name, description, status, directory)
 VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+projectColumns, NewID(), slug, fields.Name, fields.Description, fields.Status, fields.Directory))
	if err != nil {
		return Project{}, wrap("create project", err)
	}
	return p, nil
}

// Project returns a project by ID.
func (s *Store) Project(ctx context.Context, id string) (Project, error) {
	p, err := scanProject(s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id=$1`, id))
	if err != nil {
		return Project{}, wrap("read project", err)
	}
	return p, nil
}

// Projects returns all projects in stable slug order.
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY slug COLLATE "C"`)
	if err != nil {
		return nil, wrap("list projects", err)
	}
	defer rows.Close()
	out := make([]Project, 0)
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, wrap("scan project", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list projects", err)
	}
	return out, nil
}

// ProjectUpdate contains optional metadata changes. Nil fields keep stored values.
type ProjectUpdate struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	Directory   *string `json:"directory,omitempty"`
}

// UpdateProject applies a partial update under a row lock, without changing its slug.
func (s *Store) UpdateProject(ctx context.Context, id string, u ProjectUpdate) (Project, error) {
	var out Project
	err := s.tx(ctx, func(q querier) error {
		p, err := scanProject(q.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id=$1 FOR UPDATE`, id))
		if err != nil {
			return wrap("read project", err)
		}
		for _, f := range []struct {
			dst *string
			src *string
		}{{&p.Name, u.Name}, {&p.Description, u.Description}, {&p.Status, u.Status}, {&p.Directory, u.Directory}} {
			if f.src != nil {
				*f.dst = *f.src
			}
		}
		if err := p.Validate(); err != nil {
			return err
		}
		out, err = scanProject(q.QueryRow(ctx, `UPDATE projects SET name=$2, description=$3, status=$4, directory=$5,
 updated_at=clock_timestamp() WHERE id=$1 RETURNING `+projectColumns, id, p.Name, p.Description, p.Status, p.Directory))
		if err != nil {
			return wrap("update project", err)
		}
		return nil
	})
	return out, err
}

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.Directory, &p.CreatedAt, &p.UpdatedAt)
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p, err
}
