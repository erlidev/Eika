package store

import (
	"context"
	"time"

	"github.com/erlidev/eika/internal/sentry"
)

// AppendAudit adds one row to Sentry's audit log. Rows are never changed or
// removed: the table refuses it.
func (s *Store) AppendAudit(ctx context.Context, e sentry.AuditEntry) error {
	return appendAudit(ctx, s.pool, e)
}

func appendAudit(ctx context.Context, q querier, e sentry.AuditEntry) error {
	if e.ID == "" {
		e.ID = NewID()
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	detail := e.Detail
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	_, err := q.Exec(ctx, `INSERT INTO audit
        (id, at, event, session_id, run_id, subagent, routine, computer, key, kind, name, digest, reason, detail)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		e.ID, e.At, e.Event, e.Principal.Session, e.Principal.Run, e.Principal.Subagent,
		e.Principal.Routine, e.Principal.Computer, e.Key, e.Kind, e.Name, e.Digest, e.Reason, detail)
	if err != nil {
		return wrap("append audit", err)
	}
	return nil
}
