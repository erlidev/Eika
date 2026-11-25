package store

import (
	"context"

	"github.com/erlidev/eika/internal/sentry"
)

// IsBlockedHost reports whether the exact host is blocked.
func (s *Store) IsBlockedHost(ctx context.Context, host string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM blocked_hosts WHERE host = $1)`, host).Scan(&exists)
	if err != nil {
		return false, wrap("read blocked host", err)
	}
	return exists, nil
}

// BlockedHosts lists blocked hosts.
func (s *Store) BlockedHosts(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT host FROM blocked_hosts ORDER BY host`)
	if err != nil {
		return nil, wrap("list blocked hosts", err)
	}
	defer rows.Close()
	hosts := []string{}
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return nil, wrap("scan blocked host", err)
		}
		hosts = append(hosts, host)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list blocked hosts", err)
	}
	return hosts, nil
}

// SetBlockedHost changes a host and records the change together.
func (s *Store) SetBlockedHost(ctx context.Context, host string, blocked bool) error {
	return s.tx(ctx, func(q querier) error {
		var query string
		if blocked {
			query = `INSERT INTO blocked_hosts (host) VALUES ($1) ON CONFLICT DO NOTHING`
		} else {
			query = `DELETE FROM blocked_hosts WHERE host = $1`
		}
		result, err := q.Exec(ctx, query, host)
		if err != nil {
			return wrap("change blocked host", err)
		}
		if result.RowsAffected() == 0 {
			return nil
		}
		reason := "removed"
		if blocked {
			reason = "added"
		}
		return appendAudit(ctx, q, sentry.AuditEntry{Event: "setting", Kind: "blocked_host", Name: host, Reason: reason})
	})
}
