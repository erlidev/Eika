package store

import (
	"context"
	"encoding/json"
)

// Setting is one persisted value the user can change at runtime, such as the
// default model or the sandbox image. Deployment configuration stays in
// config.Config; this table holds what the UI writes.
type Setting struct {
	Key   string
	Value json.RawMessage
}

// Setting returns one setting by key.
func (s *Store) Setting(ctx context.Context, key string) (Setting, error) {
	var (
		out   = Setting{Key: key}
		value []byte
	)
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		return Setting{}, wrap("read setting "+key, err)
	}
	out.Value = json.RawMessage(value)
	return out, nil
}

// SetSetting writes a setting, replacing any value the key already has.
func (s *Store) SetSetting(ctx context.Context, key string, value json.RawMessage) error {
	const q = `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`
	if _, err := s.pool.Exec(ctx, q, key, []byte(value)); err != nil {
		return wrap("write setting "+key, err)
	}
	return nil
}

// Settings returns every setting, by key.
func (s *Store) Settings(ctx context.Context) ([]Setting, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, wrap("list settings", err)
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var (
			set   Setting
			value []byte
		)
		if err := rows.Scan(&set.Key, &value); err != nil {
			return nil, wrap("list settings", err)
		}
		set.Value = json.RawMessage(value)
		out = append(out, set)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list settings", err)
	}
	return out, nil
}
