package store

import (
	"context"
)

// SkillsComputerID returns the id recorded for the private skills installer
// computer, or ErrNotFound while none is.
func (s *Store) SkillsComputerID(ctx context.Context) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT id FROM skills_computer`).Scan(&id)
	if err != nil {
		return "", wrap("read the skills computer id", err)
	}
	return id, nil
}

// EnsureSkillsComputerID returns the skills installer's recorded id,
// recording a new one first if there is none. Callers racing record one id
// between them.
func (s *Store) EnsureSkillsComputerID(ctx context.Context) (string, error) {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO skills_computer (id) VALUES ($1) ON CONFLICT (singleton) DO NOTHING`, NewID()); err != nil {
		return "", wrap("record the skills computer id", err)
	}
	return s.SkillsComputerID(ctx)
}
