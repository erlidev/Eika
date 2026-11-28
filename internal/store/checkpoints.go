package store

import (
	"context"
	"time"
)

// Checkpoint is one commit eikad made of a project tree for a run.
type Checkpoint struct {
	ID         string
	At         time.Time
	SessionID  string
	RunID      string
	ComputerID string
	// Dir is the project tree, relative to the computer root.
	Dir string
	// Label is "start" or "end".
	Label string
	// Commit is the commit in the computer's shadow repository.
	Commit string
	// Files is how many changed paths the commit holds; Skipped how many
	// files were left out for being over 10 MB.
	Files, Skipped int
	// Took is how long taking the checkpoint took.
	Took time.Duration
}

// AddCheckpoint records a checkpoint a computer took.
func (s *Store) AddCheckpoint(ctx context.Context, c Checkpoint) error {
	if c.ID == "" {
		c.ID = NewID()
	}
	if c.At.IsZero() {
		c.At = time.Now()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO checkpoints
        (id, at, session_id, run_id, computer_id, dir, label, commit_id, files, skipped, took_ms)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		c.ID, c.At, c.SessionID, c.RunID, c.ComputerID, c.Dir, c.Label, c.Commit, c.Files, c.Skipped, c.Took.Milliseconds())
	if err != nil {
		return wrap("add checkpoint", err)
	}
	return nil
}

// RunCheckpoints returns the checkpoints taken for a run, oldest first.
func (s *Store) RunCheckpoints(ctx context.Context, runID string) ([]Checkpoint, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, at, session_id, run_id, computer_id, dir, label, commit_id, files, skipped, took_ms
 FROM checkpoints WHERE run_id = $1 ORDER BY at, id`, runID)
	if err != nil {
		return nil, wrap("list checkpoints", err)
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		var c Checkpoint
		var took int64
		if err := rows.Scan(&c.ID, &c.At, &c.SessionID, &c.RunID, &c.ComputerID, &c.Dir, &c.Label, &c.Commit, &c.Files, &c.Skipped, &took); err != nil {
			return nil, wrap("scan checkpoint", err)
		}
		c.Took = time.Duration(took) * time.Millisecond
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list checkpoints", err)
	}
	return out, nil
}
