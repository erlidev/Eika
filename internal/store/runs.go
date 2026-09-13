package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// RunState is where an agent run, or a subagent, is in its lifecycle.
type RunState string

// The states a run can be in. Every state but RunRunning is final.
const (
	// RunRunning means the agent loop is working on the session.
	RunRunning RunState = "running"
	// RunDone means the run finished on its own.
	RunDone RunState = "done"
	// RunError means the run failed; the error is recorded with it.
	RunError RunState = "error"
	// RunAborted means the run was cancelled.
	RunAborted RunState = "aborted"
)

// Run is one execution of the agent loop on a session.
type Run struct {
	ID         string
	SessionID  string
	State      RunState
	StartedAt  time.Time
	FinishedAt time.Time
	// Error is why a RunError run failed, empty otherwise.
	Error string
}

// runColumns is the column list every run query selects, in the order scanRun
// reads them.
const runColumns = `id, session_id, state, started_at, finished_at, error`

// StartRun records a run that has begun on a session.
func (s *Store) StartRun(ctx context.Context, sessionID string) (Run, error) {
	const q = `INSERT INTO runs (id, session_id, state) VALUES ($1, $2, $3) RETURNING ` + runColumns
	row := s.pool.QueryRow(ctx, q, NewID(), sessionID, RunRunning)
	r, err := scanRun(row)
	if err != nil {
		return Run{}, wrap("start run on session "+sessionID, err)
	}
	return r, nil
}

// FinishRun records how a run ended. The error message belongs to a RunError
// run and is ignored otherwise.
func (s *Store) FinishRun(ctx context.Context, id string, state RunState, runErr string) error {
	const q = `UPDATE runs SET state = $2, error = $3, finished_at = now() WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, id, state, runErr)
	if err != nil {
		return wrap("finish run "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("finish run "+id, pgx.ErrNoRows)
	}
	return nil
}

// Run returns one run by id.
func (s *Store) Run(ctx context.Context, id string) (Run, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, id)
	r, err := scanRun(row)
	if err != nil {
		return Run{}, wrap("read run "+id, err)
	}
	return r, nil
}

// Runs returns the runs of one session, oldest first.
func (s *Store) Runs(ctx context.Context, sessionID string) ([]Run, error) {
	const q = `SELECT ` + runColumns + ` FROM runs WHERE session_id = $1 ORDER BY started_at, id`
	rows, err := s.pool.Query(ctx, q, sessionID)
	if err != nil {
		return nil, wrap("list runs of session "+sessionID, err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, wrap("list runs of session "+sessionID, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list runs of session "+sessionID, err)
	}
	return out, nil
}

// scanRun reads one run row.
func scanRun(row pgx.Row) (Run, error) {
	var (
		r        Run
		finished *time.Time
	)
	if err := row.Scan(&r.ID, &r.SessionID, &r.State, &r.StartedAt, &finished, &r.Error); err != nil {
		return Run{}, err
	}
	r.StartedAt = r.StartedAt.UTC()
	r.FinishedAt = stamp(finished)
	return r, nil
}
