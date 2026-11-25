package store

import (
	"context"

	"github.com/erlidev/eika/internal/sentry"
)

// IsTrustedSource reports whether the exact host is trusted.
func (s *Store) IsTrustedSource(ctx context.Context, host string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_sources WHERE host = $1)`, host).Scan(&exists)
	if err != nil {
		return false, wrap("read trusted source", err)
	}
	return exists, nil
}

// TrustedSources lists trusted hosts.
func (s *Store) TrustedSources(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT host FROM trusted_sources ORDER BY host`)
	if err != nil {
		return nil, wrap("list trusted sources", err)
	}
	defer rows.Close()
	hosts := []string{}
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return nil, wrap("scan trusted source", err)
		}
		hosts = append(hosts, host)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list trusted sources", err)
	}
	return hosts, nil
}

// SetTrustedSource changes a host and records the change together.
func (s *Store) SetTrustedSource(ctx context.Context, host string, trusted bool) error {
	return s.tx(ctx, func(q querier) error {
		var query string
		if trusted {
			query = `INSERT INTO trusted_sources (host) VALUES ($1) ON CONFLICT DO NOTHING`
		} else {
			query = `DELETE FROM trusted_sources WHERE host = $1`
		}
		result, err := q.Exec(ctx, query, host)
		if err != nil {
			return wrap("change trusted source", err)
		}
		if result.RowsAffected() == 0 {
			return nil
		}
		reason := "removed"
		if trusted {
			reason = "added"
		}
		return appendAudit(ctx, q, sentry.AuditEntry{Event: "setting", Kind: "trusted_source", Name: host, Reason: reason})
	})
}
