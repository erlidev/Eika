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

const withheldColumns = `id, at, source_kind, source_name, session_id, run_id, call_key, content_id, text, score`

func scanWithheld(row interface{ Scan(...any) error }) (sentry.Withheld, error) {
	var w sentry.Withheld
	var kind string
	err := row.Scan(&w.ID, &w.At, &kind, &w.Source.Name, &w.Source.Session, &w.Source.Run,
		&w.Source.Key, &w.ContentID, &w.Text, &w.Score)
	w.Source.Kind = sentry.SourceKind(kind)
	return w, err
}

// Withheld returns one row, or ErrNotFound.
func (s *Store) Withheld(ctx context.Context, id string) (sentry.Withheld, error) {
	w, err := scanWithheld(s.pool.QueryRow(ctx, `SELECT `+withheldColumns+` FROM withheld WHERE id = $1`, id))
	if err != nil {
		return sentry.Withheld{}, wrap("read withheld "+id, err)
	}
	return w, nil
}

// WithheldBefore returns up to limit rows newest first, those older than the
// row named by before when it is not empty. before must exist.
func (s *Store) WithheldBefore(ctx context.Context, before string, limit int) ([]sentry.Withheld, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+withheldColumns+` FROM withheld
 WHERE $1 = '' OR (at, id) < (SELECT at, id FROM withheld WHERE id = $1)
 ORDER BY at DESC, id DESC LIMIT $2`, before, limit)
	if err != nil {
		return nil, wrap("list withheld", err)
	}
	defer rows.Close()
	var out []sentry.Withheld
	for rows.Next() {
		w, err := scanWithheld(rows)
		if err != nil {
			return nil, wrap("scan withheld", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list withheld", err)
	}
	return out, nil
}
