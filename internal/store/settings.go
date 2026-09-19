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

// setSettingQuery writes one setting, replacing any value the key has.
const setSettingQuery = `INSERT INTO settings (key, value) VALUES ($1, $2)
	ON CONFLICT (key) DO UPDATE SET value = excluded.value`

// SetSetting writes a setting, replacing any value the key already has.
func (s *Store) SetSetting(ctx context.Context, key string, value json.RawMessage) error {
	if _, err := s.pool.Exec(ctx, setSettingQuery, key, []byte(value)); err != nil {
		return wrap("write setting "+key, err)
	}
	return nil
}

// SetSettings writes several settings in one transaction: either every key
// takes its new value or, when one write fails, none does.
func (s *Store) SetSettings(ctx context.Context, values map[string]json.RawMessage) error {
	return s.tx(ctx, func(q querier) error {
		for key, value := range values {
			if _, err := q.Exec(ctx, setSettingQuery, key, []byte(value)); err != nil {
				return wrap("write setting "+key, err)
			}
		}
		return nil
	})
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
