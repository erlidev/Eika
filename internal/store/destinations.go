package store

import (
	"errors"

	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/erlidev/eika/internal/sentry"
)

// RecordDestinations notes where hosts appeared in a session. The first record
// of a host stays; a user record replaces any other.
func (s *Store) RecordDestinations(ctx context.Context, session string, hosts []string, src sentry.DestSource, name string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO destinations (session_id, host, source, source_name, at)
 SELECT $1, h, $3, $4, $5 FROM (SELECT DISTINCT h FROM unnest($2::text[]) AS h) AS hosts
 ON CONFLICT (session_id, host) DO UPDATE SET source = EXCLUDED.source, source_name = EXCLUDED.source_name, at = EXCLUDED.at
 WHERE EXCLUDED.source = 'user' AND destinations.source <> 'user'`,
		session, hosts, string(src), name, time.Now())
	if err != nil {
		return wrap("record destinations", err)
	}
	return nil
}

// DestinationSource returns a host's source in a session.
func (s *Store) DestinationSource(ctx context.Context, session, host string) (sentry.DestSource, bool, error) {
	var src string
	err := s.pool.QueryRow(ctx, `SELECT source FROM destinations WHERE session_id = $1 AND host = $2`, session, host).Scan(&src)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, wrap("read destination", err)
	}
	return sentry.DestSource(src), true, nil
}

// InheritDestinations copies a session's records to a child.
func (s *Store) InheritDestinations(ctx context.Context, parent, child string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO destinations (session_id, host, source, source_name, at)
 SELECT $2, host, source, source_name, at FROM destinations WHERE session_id = $1
 ON CONFLICT DO NOTHING`, parent, child)
	if err != nil {
		return wrap("inherit destinations", err)
	}
	return nil
}

// KnownHost reports whether a host is known to every computer or to this one.
func (s *Store) KnownHost(ctx context.Context, computer, host string) (bool, error) {
	var known bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM known_hosts
 WHERE host = $2 AND computer_id IN ('', $1))`, computer, host).Scan(&known)
	if err != nil {
		return false, wrap("read known host", err)
	}
	return known, nil
}

// MarkKnown makes a host known to one computer, or to all when computer is empty.
func (s *Store) MarkKnown(ctx context.Context, computer, host, reason string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO known_hosts (computer_id, host, reason, at)
 VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, computer, host, reason, time.Now())
	if err != nil {
		return wrap("mark host known", err)
	}
	return nil
}
