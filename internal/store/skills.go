package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/erlidev/eika/internal/skill"
)

const skillColumns = `name, enabled, sha, installed_at, description, license, compatibility,
    metadata, allowed_tools, body, files, warnings, screen, broken, source`

// UpsertSkill writes a skill's row from an install. The row is disabled and
// sound afterwards: a changed reinstall returns to disabled, and enabling is
// a separate act.
func (s *Store) UpsertSkill(ctx context.Context, r skill.Record) error {
	meta, err := json.Marshal(nonNilMap(r.Metadata))
	if err != nil {
		return wrap("record skill "+r.Name, err)
	}
	files, err := json.Marshal(nonNilSlice(r.Files))
	if err != nil {
		return wrap("record skill "+r.Name, err)
	}
	warnings, err := json.Marshal(nonNilSlice(r.Warnings))
	if err != nil {
		return wrap("record skill "+r.Name, err)
	}
	var screen []byte
	if r.Screen != nil {
		if screen, err = json.Marshal(r.Screen); err != nil {
			return wrap("record skill "+r.Name, err)
		}
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO skills
        (name, enabled, sha, installed_at, description, license, compatibility, metadata, allowed_tools, body, files, warnings, screen, broken, source)
        VALUES ($1, false, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, '', $13)
        ON CONFLICT (name) DO UPDATE SET enabled = false, sha = excluded.sha,
            installed_at = excluded.installed_at, description = excluded.description,
            license = excluded.license, compatibility = excluded.compatibility,
            metadata = excluded.metadata, allowed_tools = excluded.allowed_tools,
            body = excluded.body, files = excluded.files, warnings = excluded.warnings,
            screen = excluded.screen, broken = '', source = excluded.source`,
		r.Name, r.SHA, r.InstalledAt, r.Description, r.License, r.Compatibility, meta,
		r.AllowedTools, r.Body, files, warnings, screen, r.Source)
	if err != nil {
		return wrap("record skill "+r.Name, err)
	}
	return nil
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonNilSlice[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func scanSkill(row pgx.Row) (skill.Record, error) {
	var r skill.Record
	var installed time.Time
	var meta, files, warnings, screen []byte
	if err := row.Scan(&r.Name, &r.Enabled, &r.SHA, &installed, &r.Description, &r.License,
		&r.Compatibility, &meta, &r.AllowedTools, &r.Body, &files, &warnings, &screen, &r.Broken, &r.Source); err != nil {
		return skill.Record{}, err
	}
	r.InstalledAt = installed.UTC()
	if err := json.Unmarshal(meta, &r.Metadata); err != nil {
		return skill.Record{}, err
	}
	if err := json.Unmarshal(files, &r.Files); err != nil {
		return skill.Record{}, err
	}
	if err := json.Unmarshal(warnings, &r.Warnings); err != nil {
		return skill.Record{}, err
	}
	if len(screen) > 0 {
		r.Screen = &skill.ScreenResult{}
		if err := json.Unmarshal(screen, r.Screen); err != nil {
			return skill.Record{}, err
		}
	}
	return r, nil
}

// GetSkill returns one skill's row, or skill.ErrNotFound.
func (s *Store) GetSkill(ctx context.Context, name string) (skill.Record, error) {
	r, err := scanSkill(s.pool.QueryRow(ctx, `SELECT `+skillColumns+` FROM skills WHERE name = $1`, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return skill.Record{}, skill.ErrNotFound
	}
	if err != nil {
		return skill.Record{}, wrap("read skill "+name, err)
	}
	return r, nil
}

// ListSkills returns every skill's row, by name.
func (s *Store) ListSkills(ctx context.Context) ([]skill.Record, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+skillColumns+` FROM skills ORDER BY name`)
	if err != nil {
		return nil, wrap("list skills", err)
	}
	defer rows.Close()
	var out []skill.Record
	for rows.Next() {
		r, err := scanSkill(rows)
		if err != nil {
			return nil, wrap("list skills", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list skills", err)
	}
	return out, nil
}

// DeleteSkill removes a skill's row, if there is one.
func (s *Store) DeleteSkill(ctx context.Context, name string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM skills WHERE name = $1`, name); err != nil {
		return wrap("delete skill "+name, err)
	}
	return nil
}

// SetSkillBroken sets why a skill's directory no longer matches its row, or
// clears it with "".
func (s *Store) SetSkillBroken(ctx context.Context, name, reason string) error {
	if _, err := s.pool.Exec(ctx, `UPDATE skills SET broken = $2 WHERE name = $1`, name, reason); err != nil {
		return wrap("mark skill "+name, err)
	}
	return nil
}

// SetSkillEnabled sets whether the user has enabled a skill. It is skill.ErrNotFound
// for a name with no row.
func (s *Store) SetSkillEnabled(ctx context.Context, name string, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE skills SET enabled = $2 WHERE name = $1`, name, enabled)
	if err != nil {
		return wrap("set skill "+name+" enabled", err)
	}
	if tag.RowsAffected() == 0 {
		return skill.ErrNotFound
	}
	return nil
}
