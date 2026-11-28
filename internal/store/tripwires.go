package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/erlidev/eika/internal/sentry"
)

// Audit events of the tripwires: one row when one fires, and one for each
// answer. Settings, Security counts them (TripwireCounts).
const (
	AuditTripwire     = "tripwire"
	AuditTripwireFine = "tripwire_fine"
	AuditTripwireStop = "tripwire_stop"
)

// Tripwire entry states.
const (
	TripwireOpen    = "open"
	TripwireFine    = "fine"
	TripwireStopped = "stopped"
)

// TripwireEntry is one Inbox entry a tripwire made.
type TripwireEntry struct {
	ID         string
	At         time.Time
	Tripwire   string
	Session    string
	Run        string
	Computer   string
	Host       string
	Source     string // where the host first appeared: user, search, content or model
	Held       bool
	Summary    string
	Status     string
	AnsweredAt *time.Time
}

// TripwireCount is how often a tripwire fired and how often the answer was
// "this was fine".
type TripwireCount struct {
	Tripwire string
	Fired    int
	Fine     int
}

const tripwireColumns = `id, at, tripwire, session_id, run_id, computer_id, host, source, held, summary, status, answered_at`

func scanTripwire(row interface{ Scan(...any) error }) (TripwireEntry, error) {
	var e TripwireEntry
	err := row.Scan(&e.ID, &e.At, &e.Tripwire, &e.Session, &e.Run, &e.Computer, &e.Host, &e.Source, &e.Held, &e.Summary, &e.Status, &e.AnsweredAt)
	return e, err
}

// AddTripwireEntry stores an open entry and its audit row together.
func (s *Store) AddTripwireEntry(ctx context.Context, e TripwireEntry) (TripwireEntry, error) {
	e.ID, e.Status = NewID(), TripwireOpen
	if e.At.IsZero() {
		e.At = time.Now()
	}
	detail, _ := json.Marshal(map[string]any{"entry": e.ID, "host": e.Host, "held": e.Held})
	err := s.tx(ctx, func(q querier) error {
		_, err := q.Exec(ctx, `INSERT INTO tripwire_entries (`+tripwireColumns+`)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULL)`,
			e.ID, e.At, e.Tripwire, e.Session, e.Run, e.Computer, e.Host, e.Source, e.Held, e.Summary, e.Status)
		if err != nil {
			return wrap("add tripwire entry", err)
		}
		return appendAudit(ctx, q, sentry.AuditEntry{Event: AuditTripwire, Principal: sentry.Principal{Session: e.Session, Run: e.Run, Computer: e.Computer},
			Kind: "tripwire", Name: e.Tripwire, Detail: detail})
	})
	return e, err
}

// TripwireEntry returns one entry.
func (s *Store) TripwireEntry(ctx context.Context, id string) (TripwireEntry, error) {
	e, err := scanTripwire(s.pool.QueryRow(ctx, `SELECT `+tripwireColumns+` FROM tripwire_entries WHERE id = $1`, id))
	if err != nil {
		return TripwireEntry{}, wrap("read tripwire entry", err)
	}
	return e, nil
}

// OpenTripwireEntries lists the entries nobody has answered, newest first.
func (s *Store) OpenTripwireEntries(ctx context.Context, limit int) ([]TripwireEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+tripwireColumns+` FROM tripwire_entries
 WHERE status = 'open' ORDER BY at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, wrap("list tripwire entries", err)
	}
	defer rows.Close()
	out := []TripwireEntry{}
	for rows.Next() {
		e, err := scanTripwire(rows)
		if err != nil {
			return nil, wrap("scan tripwire entry", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list tripwire entries", err)
	}
	return out, nil
}

// CountOpenTripwireEntries counts the entries nobody has answered.
func (s *Store) CountOpenTripwireEntries(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tripwire_entries WHERE status = 'open'`).Scan(&n); err != nil {
		return 0, wrap("count tripwire entries", err)
	}
	return n, nil
}

// AnswerTripwireEntry records an answer ("fine" or "stopped") and its audit
// row together. It returns ErrConflict for an entry that was answered already,
// and ErrNotFound for one that does not exist.
func (s *Store) AnswerTripwireEntry(ctx context.Context, id, status string) (TripwireEntry, error) {
	event := AuditTripwireFine
	if status == TripwireStopped {
		event = AuditTripwireStop
	}
	var out TripwireEntry
	err := s.tx(ctx, func(q querier) error {
		e, err := scanTripwire(q.QueryRow(ctx, `UPDATE tripwire_entries SET status = $2, answered_at = $3
 WHERE id = $1 AND status = 'open' RETURNING `+tripwireColumns, id, status, time.Now()))
		if err != nil {
			if _, err := scanTripwire(q.QueryRow(ctx, `SELECT `+tripwireColumns+` FROM tripwire_entries WHERE id = $1`, id)); err == nil {
				return wrap("answer tripwire entry", ErrConflict)
			}
			return wrap("answer tripwire entry", err)
		}
		out = e
		detail, _ := json.Marshal(map[string]any{"entry": e.ID, "host": e.Host})
		return appendAudit(ctx, q, sentry.AuditEntry{Event: event, Principal: sentry.Principal{Session: e.Session, Run: e.Run, Computer: e.Computer},
			Kind: "tripwire", Name: e.Tripwire, Detail: detail})
	})
	return out, err
}

// TripwireCounts reads from the audit log how often each tripwire fired and
// how often the answer was "this was fine".
func (s *Store) TripwireCounts(ctx context.Context) ([]TripwireCount, error) {
	rows, err := s.pool.Query(ctx, `SELECT name,
 count(*) FILTER (WHERE event = $1), count(*) FILTER (WHERE event = $2)
 FROM audit WHERE event IN ($1, $2) GROUP BY name ORDER BY name`, AuditTripwire, AuditTripwireFine)
	if err != nil {
		return nil, wrap("count tripwires", err)
	}
	defer rows.Close()
	out := []TripwireCount{}
	for rows.Next() {
		var c TripwireCount
		if err := rows.Scan(&c.Tripwire, &c.Fired, &c.Fine); err != nil {
			return nil, wrap("scan tripwire count", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("count tripwires", err)
	}
	return out, nil
}
