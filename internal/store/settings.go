package store

import (
	"context"
	"encoding/json"

	"github.com/erlidev/eika/internal/sentry"
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

// SetSettings writes several settings in one transaction: either every key
// takes its new value or, when one write fails, none does.
func (s *Store) SetSettings(ctx context.Context, values map[string]json.RawMessage) error {
	return s.tx(ctx, func(q querier) error {
		_, required := values["screen_required"]
		_, threshold := values["screen_threshold"]
		_, memoryThreshold := values["memory_screen_threshold"]
		_, watchThreshold := values["watch_threshold"]
		_, egress := values["sandbox_egress"]
		if required || threshold || memoryThreshold || watchThreshold || egress {
			if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('eika security settings'))`); err != nil {
				return wrap("lock security settings", err)
			}
		}
		for key, value := range values {
			if key == "screen_required" || key == "screen_threshold" || key == "memory_screen_threshold" || key == "watch_threshold" || key == "sandbox_egress" {
				var changed bool
				err := q.QueryRow(ctx, `SELECT COALESCE((SELECT value IS DISTINCT FROM $2::jsonb FROM settings WHERE key = $1), true)`, key, []byte(value)).Scan(&changed)
				if err != nil {
					return wrap("compare setting "+key, err)
				}
				if changed {
					detail, err := json.Marshal(map[string]json.RawMessage{"value": value})
					if err != nil {
						return wrap("encode setting audit", err)
					}
					if err := appendAudit(ctx, q, sentry.AuditEntry{Event: "setting", Name: key, Detail: detail}); err != nil {
						return err
					}
				}
			}
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
