package store

import (
	"context"
	"time"

	"github.com/erlidev/eika/internal/sentry"
)

// AppendMark records a mark. The same kind and value on one run is kept once.
func (s *Store) AppendMark(ctx context.Context, m sentry.Mark) error {
	if m.ID == "" {
		m.ID = NewID()
	}
	if m.At.IsZero() {
		m.At = time.Now()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO marks
        (id, at, session_id, run_id, kind, value, source_name, call_key, score)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
        ON CONFLICT (session_id, run_id, kind, value) DO NOTHING`,
		m.ID, m.At, m.Session, m.Run, string(m.Kind), m.Value, m.SourceName, m.Key, m.Score)
	if err != nil {
		return wrap("append mark", err)
	}
	return nil
}

// WatchedHosts returns the hosts a session's ingest marked watched.
func (s *Store) WatchedHosts(ctx context.Context, session string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT value FROM marks
 WHERE session_id = $1 AND kind = $2 AND value <> '' ORDER BY value`, session, string(sentry.MarkWatched))
	if err != nil {
		return nil, wrap("list watched hosts", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return nil, wrap("scan watched host", err)
		}
		out = append(out, host)
	}
	return out, wrap("list watched hosts", rows.Err())
}
