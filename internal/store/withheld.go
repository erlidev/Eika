package store

import (
	"context"
	"time"

	"github.com/erlidev/eika/internal/sentry"
)

// AppendWithheld keeps the text a removal kept out of a session's context.
func (s *Store) AppendWithheld(ctx context.Context, w sentry.Withheld) error {
	if w.ID == "" {
		w.ID = NewID()
	}
	if w.At.IsZero() {
		w.At = time.Now()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO withheld
        (id, at, source_kind, source_name, session_id, run_id, call_key, content_id, text, score)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		w.ID, w.At, string(w.Source.Kind), w.Source.Name, w.Source.Session, w.Source.Run,
		w.Source.Key, w.ContentID, w.Text, w.Score)
	if err != nil {
		return wrap("append withheld", err)
	}
	return nil
}
