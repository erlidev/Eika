package store

import (
	"context"
)

// MemoryComputerID returns the id recorded for the private memory computer,
// or ErrNotFound while none is.
func (s *Store) MemoryComputerID(ctx context.Context) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT id FROM memory_computer`).Scan(&id)
	if err != nil {
		return "", wrap("read the memory computer id", err)
	}
	return id, nil
}

// EnsureMemoryComputerID returns the memory computer's recorded id, recording
// a new one first if there is none. Callers racing record one id between them.
func (s *Store) EnsureMemoryComputerID(ctx context.Context) (string, error) {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO memory_computer (id) VALUES ($1) ON CONFLICT (singleton) DO NOTHING`, NewID()); err != nil {
		return "", wrap("record the memory computer id", err)
	}
	return s.MemoryComputerID(ctx)
}
