package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound reports that a row the caller asked for does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict reports that a write collided with an existing row, such as a
// second project with the same name.
var ErrConflict = errors.New("conflict")

// Store is the harness's database. One Store owns one connection pool and is
// safe for concurrent use.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to the PostgreSQL database at url, verifies the connection,
// and applies any migration the database is missing. Opening an
// already-migrated database is a no-op, so every harness start takes the same
// path.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the connection pool. A closed Store cannot be reused.
func (s *Store) Close() { s.pool.Close() }

// querier is the part of the pgx API a query needs, so that the same code
// runs on the pool and inside a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// tx runs fn in a transaction and commits it when fn returns nil.
func (s *Store) tx(ctx context.Context, fn func(q querier) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wrap("begin transaction", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return wrap("commit transaction", err)
	}
	return nil
}

// uniqueViolation is the SQLSTATE PostgreSQL reports for a duplicate key.
const uniqueViolation = "23505"

// foreignKeyViolation is the SQLSTATE for a row that names a missing parent.
const foreignKeyViolation = "23503"

// wrap names the operation that failed and maps the pgx errors callers act on
// onto this package's sentinels.
func wrap(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return fmt.Errorf("%s: %w", op, ErrConflict)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// nullable turns an empty optional column value into a SQL NULL, which is how
// this schema spells "no parent" and "no head".
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// text reads a nullable text column, reporting SQL NULL as the empty string.
func text(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// stamp reads a nullable timestamp as UTC, reporting SQL NULL as the zero
// time.
func stamp(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.UTC()
}
