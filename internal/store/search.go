package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// SearchKey is the API key of a search provider, or GitHub's token.
type SearchKey struct {
	Name string
	// Key is the key as the harness sealed it. The store never sees it in
	// the clear.
	Key       []byte
	UpdatedAt time.Time
}

// SearchKey returns the key stored under name, ErrNotFound when there is
// none.
func (s *Store) SearchKey(ctx context.Context, name string) (SearchKey, error) {
	k := SearchKey{Name: name}
	err := s.pool.QueryRow(ctx, `SELECT key, updated_at FROM search_keys WHERE name = $1`, name).Scan(&k.Key, &k.UpdatedAt)
	if err != nil {
		return SearchKey{}, wrap("read search key "+name, err)
	}
	k.UpdatedAt = k.UpdatedAt.UTC()
	return k, nil
}

// SearchKeys returns every stored key, by name.
func (s *Store) SearchKeys(ctx context.Context) ([]SearchKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, key, updated_at FROM search_keys ORDER BY name`)
	if err != nil {
		return nil, wrap("list search keys", err)
	}
	defer rows.Close()
	var out []SearchKey
	for rows.Next() {
		var k SearchKey
		if err := rows.Scan(&k.Name, &k.Key, &k.UpdatedAt); err != nil {
			return nil, wrap("list search keys", err)
		}
		k.UpdatedAt = k.UpdatedAt.UTC()
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list search keys", err)
	}
	return out, nil
}

// SetSearchKey stores a sealed key under name, replacing any it had.
func (s *Store) SetSearchKey(ctx context.Context, name string, key []byte) error {
	const q = `INSERT INTO search_keys (name, key) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET key = excluded.key, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, name, key); err != nil {
		return wrap("write search key "+name, err)
	}
	return nil
}

// DeleteSearchKey removes the key stored under name. Removing a key that is
// not there is not an error: the outcome is the same.
func (s *Store) DeleteSearchKey(ctx context.Context, name string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM search_keys WHERE name = $1`, name); err != nil {
		return wrap("delete search key "+name, err)
	}
	return nil
}

// SearchUsage is one quota bucket's counters and cooldown.
type SearchUsage struct {
	Name      string
	Day       string
	DayUsed   int
	Month     string
	MonthUsed int
	// CooldownUntil is zero when the bucket is not cooling down.
	CooldownUntil time.Time
	FailStreak    int
}

// SearchUsage returns every bucket's counters.
func (s *Store) SearchUsage(ctx context.Context) ([]SearchUsage, error) {
	const q = `SELECT name, day, day_used, month, month_used, cooldown_until, fail_streak
		FROM search_usage ORDER BY name`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, wrap("list search usage", err)
	}
	defer rows.Close()
	var out []SearchUsage
	for rows.Next() {
		u, err := scanSearchUsage(rows)
		if err != nil {
			return nil, wrap("list search usage", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list search usage", err)
	}
	return out, nil
}

// SetSearchUsage writes a bucket's counters, replacing what it had.
func (s *Store) SetSearchUsage(ctx context.Context, u SearchUsage) error {
	var cooldown *time.Time
	if !u.CooldownUntil.IsZero() {
		cooldown = &u.CooldownUntil
	}
	const q = `INSERT INTO search_usage (name, day, day_used, month, month_used, cooldown_until, fail_streak)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (name) DO UPDATE SET day = excluded.day, day_used = excluded.day_used,
			month = excluded.month, month_used = excluded.month_used,
			cooldown_until = excluded.cooldown_until, fail_streak = excluded.fail_streak, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, u.Name, u.Day, u.DayUsed, u.Month, u.MonthUsed, cooldown, u.FailStreak); err != nil {
		return wrap("write search usage "+u.Name, err)
	}
	return nil
}

// scanSearchUsage reads one usage row.
func scanSearchUsage(row pgx.Row) (SearchUsage, error) {
	var (
		u        SearchUsage
		cooldown *time.Time
	)
	if err := row.Scan(&u.Name, &u.Day, &u.DayUsed, &u.Month, &u.MonthUsed, &cooldown, &u.FailStreak); err != nil {
		return SearchUsage{}, err
	}
	if cooldown != nil {
		u.CooldownUntil = cooldown.UTC()
	}
	return u, nil
}
