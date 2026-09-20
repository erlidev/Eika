package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// PasswordHash returns the stored sign-in password hash. A harness nobody has
// set up yet has none, which is ErrNotFound.
func (s *Store) PasswordHash(ctx context.Context) (string, error) {
	var hash string
	if err := s.pool.QueryRow(ctx, `SELECT hash FROM auth_password WHERE id = 1`).Scan(&hash); err != nil {
		return "", wrap("read password", err)
	}
	return hash, nil
}

// CreatePasswordHash stores the first sign-in password. A harness that has
// one already is ErrConflict, which is what keeps setup to a single claim
// however many browsers race for it.
func (s *Store) CreatePasswordHash(ctx context.Context, hash string) error {
	if _, err := s.pool.Exec(ctx, `INSERT INTO auth_password (id, hash) VALUES (1, $1)`, hash); err != nil {
		return wrap("create password", err)
	}
	return nil
}

// ReplacePasswordHash stores a new sign-in password and ends every session,
// in one transaction, so that no browser signed in with the old password
// outlives the change.
func (s *Store) ReplacePasswordHash(ctx context.Context, hash string) error {
	return s.tx(ctx, func(q querier) error {
		tag, err := q.Exec(ctx, `UPDATE auth_password SET hash = $1, updated_at = now() WHERE id = 1`, hash)
		if err != nil {
			return wrap("replace password", err)
		}
		if tag.RowsAffected() == 0 {
			return wrap("replace password", pgx.ErrNoRows)
		}
		if _, err := q.Exec(ctx, `DELETE FROM auth_sessions`); err != nil {
			return wrap("end sessions", err)
		}
		return nil
	})
}

// CreateAuthSession records a signed-in browser by the hash of its token.
// Sessions that have expired are removed on the way, so the table holds live
// sessions only.
func (s *Store) CreateAuthSession(ctx context.Context, tokenHash string, expires time.Time) error {
	return s.tx(ctx, func(q querier) error {
		if _, err := q.Exec(ctx, `DELETE FROM auth_sessions WHERE expires_at <= now()`); err != nil {
			return wrap("prune sessions", err)
		}
		if _, err := q.Exec(ctx, `INSERT INTO auth_sessions (token_hash, expires_at) VALUES ($1, $2)`,
			tokenHash, expires.UTC()); err != nil {
			return wrap("create session", err)
		}
		return nil
	})
}

// AuthSessionExpiry returns when the session with the given token hash
// expires. An unknown session is ErrNotFound; an expired one is reported with
// its past expiry, and the caller compares.
func (s *Store) AuthSessionExpiry(ctx context.Context, tokenHash string) (time.Time, error) {
	var expires time.Time
	err := s.pool.QueryRow(ctx, `SELECT expires_at FROM auth_sessions WHERE token_hash = $1`, tokenHash).Scan(&expires)
	if err != nil {
		return time.Time{}, wrap("read session", err)
	}
	return expires.UTC(), nil
}

// DeleteAuthSession ends one session. Ending a session that is already gone
// is not an error: signing out twice leaves the browser signed out either
// way.
func (s *Store) DeleteAuthSession(ctx context.Context, tokenHash string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM auth_sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return wrap("delete session", err)
	}
	return nil
}
