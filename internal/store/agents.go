package store

import (
	"context"
	"fmt"

	"github.com/erlidev/eika/internal/agentdef"
)

// AgentOverrides returns stored overrides, keyed by built-in name.
func (s *Store) AgentOverrides(ctx context.Context) (map[string]agentdef.Override, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, class, prompt FROM agents ORDER BY name`)
	if err != nil {
		return nil, wrap("list agent overrides", err)
	}
	defer rows.Close()
	out := make(map[string]agentdef.Override)
	for rows.Next() {
		var name string
		var o agentdef.Override
		if err := rows.Scan(&name, &o.Class, &o.Prompt); err != nil {
			return nil, wrap("scan agent override", err)
		}
		out[name] = o
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list agent overrides", err)
	}
	return out, nil
}

// SetAgentOverride replaces the named built-in's overrides. Empty overrides reset it.
func (s *Store) SetAgentOverride(ctx context.Context, name string, o agentdef.Override) error {
	if _, ok := agentdef.Lookup(name); !ok {
		return fmt.Errorf("set agent override %s: %w", name, ErrNotFound)
	}
	if err := o.Validate(); err != nil {
		return fmt.Errorf("set agent override: %w", err)
	}
	if o.Class == nil && o.Prompt == nil {
		_, err := s.pool.Exec(ctx, `DELETE FROM agents WHERE name = $1`, name)
		if err != nil {
			return wrap("reset agent override", err)
		}
		return nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO agents (name, class, prompt) VALUES ($1, $2, $3)
 ON CONFLICT (name) DO UPDATE SET class = EXCLUDED.class, prompt = EXCLUDED.prompt`, name, o.Class, o.Prompt)
	if err != nil {
		return wrap("set agent override", err)
	}
	return nil
}
