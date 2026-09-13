package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Subagent is one child agent run: its own session in its own workspace,
// reporting back to the session that spawned it.
type Subagent struct {
	ID              string
	ParentSessionID string
	ChildSessionID  string
	// ChildWorkspaceID is the workspace the child works in, cloned from the
	// parent's at the commit the spawning entry recorded.
	ChildWorkspaceID string
	State            RunState
	// Result is the summary the child reported back, which the parent sees as
	// a tool result.
	Result     string
	CreatedAt  time.Time
	FinishedAt time.Time
}

// subagentColumns is the column list every subagent query selects, in the
// order scanSubagent reads them.
const subagentColumns = `id, parent_session_id, child_session_id, child_workspace_id,
	state, result, created_at, finished_at`

// StartSubagent records a child agent that has been spawned.
func (s *Store) StartSubagent(ctx context.Context, sub Subagent) (Subagent, error) {
	if sub.ID == "" {
		sub.ID = NewID()
	}
	const q = `INSERT INTO subagents
		(id, parent_session_id, child_session_id, child_workspace_id, state)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + subagentColumns
	row := s.pool.QueryRow(ctx, q, sub.ID, sub.ParentSessionID, sub.ChildSessionID,
		sub.ChildWorkspaceID, RunRunning)
	out, err := scanSubagent(row)
	if err != nil {
		return Subagent{}, wrap("start subagent for session "+sub.ParentSessionID, err)
	}
	return out, nil
}

// FinishSubagent records how a child agent ended and what it reported.
func (s *Store) FinishSubagent(ctx context.Context, id string, state RunState, result string) error {
	const q = `UPDATE subagents SET state = $2, result = $3, finished_at = now() WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id, state, result)
	if err != nil {
		return wrap("finish subagent "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("finish subagent "+id, pgx.ErrNoRows)
	}
	return nil
}

// Subagent returns one subagent by id.
func (s *Store) Subagent(ctx context.Context, id string) (Subagent, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+subagentColumns+` FROM subagents WHERE id = $1`, id)
	sub, err := scanSubagent(row)
	if err != nil {
		return Subagent{}, wrap("read subagent "+id, err)
	}
	return sub, nil
}

// Subagents returns the children one session spawned, oldest first.
func (s *Store) Subagents(ctx context.Context, parentSessionID string) ([]Subagent, error) {
	const q = `SELECT ` + subagentColumns + ` FROM subagents
		WHERE parent_session_id = $1 ORDER BY created_at, id`
	rows, err := s.pool.Query(ctx, q, parentSessionID)
	if err != nil {
		return nil, wrap("list subagents of session "+parentSessionID, err)
	}
	defer rows.Close()
	var out []Subagent
	for rows.Next() {
		sub, err := scanSubagent(rows)
		if err != nil {
			return nil, wrap("list subagents of session "+parentSessionID, err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list subagents of session "+parentSessionID, err)
	}
	return out, nil
}

// scanSubagent reads one subagent row.
func scanSubagent(row pgx.Row) (Subagent, error) {
	var (
		sub      Subagent
		finished *time.Time
	)
	err := row.Scan(&sub.ID, &sub.ParentSessionID, &sub.ChildSessionID, &sub.ChildWorkspaceID,
		&sub.State, &sub.Result, &sub.CreatedAt, &finished)
	if err != nil {
		return Subagent{}, err
	}
	sub.CreatedAt = sub.CreatedAt.UTC()
	sub.FinishedAt = stamp(finished)
	return sub, nil
}
