package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Computer is the persisted record of one sandbox container. The container
// itself is owned by internal/computer; this row is what survives a harness
// restart and what the UI lists.
type Computer struct {
	ID string
	// Name is the human-readable name the UI shows.
	Name string
	// Image is the container image the computer runs.
	Image string
	// State mirrors computer.State: creating, running, stopped, or gone.
	State string
	// ContainerID is the Docker container id, empty before the container
	// exists.
	ContainerID string
	// Sandbox is what the container may consume, reach, and expose.
	Sandbox ComputerSandbox
	// Pinned computers are listed before the others.
	Pinned bool
	// Archived computers are set aside under a heading of their own. It is
	// filing only: the container is neither stopped nor started by it.
	Archived bool
	// Home marks the one computer that holds the user's durable files. At
	// most one row has it.
	Home      bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ComputerSandbox is what a computer's container may consume and reach. The
// zero value is a computer with no limits and open egress.
type ComputerSandbox struct {
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
}

// computerColumns is the column list every computer query selects, in the
// order scanComputer reads them.
const computerColumns = `id, name, image, state, container_id, sandbox, pinned, archived,
	home, created_at, updated_at`

// CreateComputer inserts w and returns it with the fields the database
// assigned. An empty ID gets a fresh one.
func (s *Store) CreateComputer(ctx context.Context, w Computer) (Computer, error) {
	if w.ID == "" {
		w.ID = NewID()
	}
	sandbox, err := json.Marshal(w.Sandbox)
	if err != nil {
		return Computer{}, fmt.Errorf("encode sandbox of computer %s: %w", w.ID, err)
	}
	const q = `INSERT INTO computers
		(id, name, image, state, container_id, sandbox, pinned, archived, home)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + computerColumns
	row := s.pool.QueryRow(ctx, q, w.ID, w.Name, w.Image, w.State, w.ContainerID, sandbox,
		w.Pinned, w.Archived, w.Home)
	out, err := scanComputer(row)
	if err != nil {
		return Computer{}, wrap("create computer "+w.ID, err)
	}
	return out, nil
}

// EnsureHomeComputer returns the home computer, inserting w as that row first
// when there is none. Concurrent callers, and callers in separate processes,
// get the same row: the database allows one home, so the insert of every
// caller but one does nothing. Created reports whether this call inserted it,
// in which case the row has w's id.
func (s *Store) EnsureHomeComputer(ctx context.Context, w Computer) (home Computer, created bool, err error) {
	if w.ID == "" {
		w.ID = NewID()
	}
	sandbox, err := json.Marshal(w.Sandbox)
	if err != nil {
		return Computer{}, false, fmt.Errorf("encode sandbox of computer %s: %w", w.ID, err)
	}
	const insert = `INSERT INTO computers
		(id, name, image, state, container_id, sandbox, pinned, archived, home)
		VALUES ($1, $2, $3, $4, $5, $6, false, false, true)
		ON CONFLICT (home) WHERE home DO NOTHING`
	tag, err := s.pool.Exec(ctx, insert, w.ID, w.Name, w.Image, w.State, w.ContainerID, sandbox)
	if err != nil {
		return Computer{}, false, wrap("ensure home computer", err)
	}
	home, err = s.HomeComputer(ctx)
	if err != nil {
		return Computer{}, false, err
	}
	return home, tag.RowsAffected() == 1, nil
}

// HomeComputer returns the home computer, or ErrNotFound while the harness
// has not recorded it yet.
func (s *Store) HomeComputer(ctx context.Context) (Computer, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+computerColumns+` FROM computers WHERE home`)
	w, err := scanComputer(row)
	if err != nil {
		return Computer{}, wrap("read home computer", err)
	}
	return w, nil
}

// Computer returns the computer with the given id.
func (s *Store) Computer(ctx context.Context, id string) (Computer, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+computerColumns+` FROM computers WHERE id = $1`, id)
	w, err := scanComputer(row)
	if err != nil {
		return Computer{}, wrap("read computer "+id, err)
	}
	return w, nil
}

// Computers returns every computer, oldest first.
func (s *Store) Computers(ctx context.Context) ([]Computer, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+computerColumns+` FROM computers ORDER BY created_at, id`)
	if err != nil {
		return nil, wrap("list computers", err)
	}
	defer rows.Close()
	var out []Computer
	for rows.Next() {
		w, err := scanComputer(rows)
		if err != nil {
			return nil, wrap("list computers", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list computers", err)
	}
	return out, nil
}

// SetComputerState records the state a computer's container is in and the
// container it runs as. An empty containerID leaves the recorded one alone,
// because a stopped container keeps its id.
func (s *Store) SetComputerState(ctx context.Context, id, state, containerID string) error {
	const q = `UPDATE computers
		SET state = $2,
		    container_id = CASE WHEN $3 = '' THEN container_id ELSE $3 END,
		    updated_at = now()
		WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id, state, containerID)
	if err != nil {
		return wrap("set computer state "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("set computer state "+id, pgx.ErrNoRows)
	}
	return nil
}

// SetComputerSandbox records what a computer's container may consume,
// reach, and expose, and returns the computer as it now stands.
func (s *Store) SetComputerSandbox(ctx context.Context, id string, sandbox ComputerSandbox) (Computer, error) {
	encoded, err := json.Marshal(sandbox)
	if err != nil {
		return Computer{}, fmt.Errorf("encode sandbox of computer %s: %w", id, err)
	}
	row := s.pool.QueryRow(ctx, `UPDATE computers SET sandbox = $2, updated_at = now()
		WHERE id = $1 RETURNING `+computerColumns, id, encoded)
	w, err := scanComputer(row)
	if err != nil {
		return Computer{}, wrap("set sandbox of computer "+id, err)
	}
	return w, nil
}

// ComputerChanges are the edits a user makes to a computer's name and
// filing. A nil field is left as it is.
type ComputerChanges struct {
	Name     *string
	Pinned   *bool
	Archived *bool
}

// UpdateComputer applies changes to a computer and returns it as it now
// stands.
func (s *Store) UpdateComputer(ctx context.Context, id string, changes ComputerChanges) (Computer, error) {
	const q = `UPDATE computers
		SET name = COALESCE($2, name),
		    pinned = COALESCE($3, pinned),
		    archived = COALESCE($4, archived),
		    updated_at = now()
		WHERE id = $1
		RETURNING ` + computerColumns
	w, err := scanComputer(s.pool.QueryRow(ctx, q, id, changes.Name, changes.Pinned, changes.Archived))
	if err != nil {
		return Computer{}, wrap("update computer "+id, err)
	}
	return w, nil
}

// DeleteComputer removes a computer and its sessions.
func (s *Store) DeleteComputer(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM computers WHERE id = $1`, id)
	if err != nil {
		return wrap("delete computer "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("delete computer "+id, pgx.ErrNoRows)
	}
	return nil
}

// scanComputer reads one computer row.
func scanComputer(row pgx.Row) (Computer, error) {
	var (
		w       Computer
		sandbox []byte
	)
	err := row.Scan(&w.ID, &w.Name, &w.Image, &w.State, &w.ContainerID, &sandbox,
		&w.Pinned, &w.Archived, &w.Home, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Computer{}, err
	}
	if err := json.Unmarshal(sandbox, &w.Sandbox); err != nil {
		return Computer{}, fmt.Errorf("decode sandbox of computer %s: %w", w.ID, err)
	}
	w.CreatedAt = w.CreatedAt.UTC()
	w.UpdatedAt = w.UpdatedAt.UTC()
	return w, nil
}
