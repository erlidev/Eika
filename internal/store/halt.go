package store

import (
	"context"
	"time"

	"github.com/erlidev/eika/internal/sentry"
)

// Halt returns whether Eika is halted.
func (s *Store) Halt(ctx context.Context) (sentry.HaltState, error) {
	var (
		st sentry.HaltState
		at *time.Time
	)
	err := s.pool.QueryRow(ctx, `SELECT halted, at, reason FROM halt`).Scan(&st.Halted, &at, &st.Reason)
	if err != nil {
		return sentry.HaltState{}, wrap("read halt", err)
	}
	if at != nil {
		st.At = *at
	}
	return st, nil
}

// SetHalt records the halted state and the audit row for the change in one
// transaction, so neither exists without the other.
func (s *Store) SetHalt(ctx context.Context, st sentry.HaltState, audit sentry.AuditEntry) error {
	return s.tx(ctx, func(q querier) error {
		var at *time.Time
		if !st.At.IsZero() {
			at = &st.At
		}
		if _, err := q.Exec(ctx, `UPDATE halt SET halted = $1, at = $2, reason = $3`, st.Halted, at, st.Reason); err != nil {
			return wrap("write halt", err)
		}
		return appendAudit(ctx, q, audit)
	})
}
