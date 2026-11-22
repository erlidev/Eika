package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/erlidev/eika/internal/memory"
	"github.com/jackc/pgx/v5"
)

// UpsertMemory writes the index row for a memory file.
func (s *Store) UpsertMemory(ctx context.Context, r memory.Record) error {
	created, err := time.Parse(time.RFC3339, r.Created)
	if err != nil {
		return wrap("index memory "+r.Name, err)
	}
	updated, err := time.Parse(time.RFC3339, r.Updated)
	if err != nil {
		return wrap("index memory "+r.Name, err)
	}
	var expires *string
	if r.Expires != "" {
		expires = &r.Expires
	}
	// The screen verdict belongs to the pending write: archiving and a
	// reindex of unchanged content (no verdict given) keep it.
	var screen *string
	if r.Screen != nil {
		b, err := json.Marshal(r.Screen)
		if err != nil {
			return wrap("index memory "+r.Name, err)
		}
		v := string(b)
		screen = &v
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO memories
        (name, description, type, source, created_at, updated_at, expires, pinned, state, content_sha, body, screen, archive_reason)
        VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8, $9, $10, $11, $12::jsonb, $13)
        ON CONFLICT (name) DO UPDATE SET description = excluded.description, type = excluded.type,
            source = excluded.source, created_at = excluded.created_at, updated_at = excluded.updated_at,
            expires = excluded.expires, pinned = excluded.pinned, state = excluded.state,
            content_sha = excluded.content_sha, body = excluded.body,
            screen = CASE WHEN excluded.state = 'archived' OR memories.content_sha = excluded.content_sha
                THEN memories.screen ELSE excluded.screen END,
            archive_reason = CASE WHEN excluded.state = 'archived'
                THEN COALESCE(NULLIF(excluded.archive_reason, ''), memories.archive_reason) ELSE '' END`,
		r.Name, r.Description, r.Type, r.Source, created, updated, expires, r.Pinned, r.State, r.ContentSHA, r.Body, screen, r.Reason)
	if err != nil {
		return wrap("index memory "+r.Name, err)
	}
	return nil
}

// SearchMemories ranks memories against a plain-text query, best first and
// then by name. Archived ones are included only when asked for.
func (s *Store) SearchMemories(ctx context.Context, q memory.Query, snippetRunes int) ([]memory.Hit, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, description, type, state, pinned, left(body, $5),
            ts_rank(body_tsv, query) AS rank
        FROM memories, plainto_tsquery('english', $1) query
        WHERE body_tsv @@ query
          AND ($2 = '' OR type = $2)
          AND ($3 OR state <> 'archived')
        ORDER BY rank DESC, name
        LIMIT $4`, q.Text, q.Type, q.IncludeArchived, q.Limit, snippetRunes)
	if err != nil {
		return nil, wrap("search memories", err)
	}
	defer rows.Close()
	var out []memory.Hit
	for rows.Next() {
		var h memory.Hit
		var rank float32
		if err := rows.Scan(&h.Name, &h.Description, &h.Type, &h.State, &h.Pinned, &h.Snippet, &rank); err != nil {
			return nil, wrap("search memories", err)
		}
		h.Rank = float64(rank)
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("search memories", err)
	}
	return out, nil
}

// MemorySHAs returns every indexed memory's content_sha by name.
func (s *Store) MemorySHAs(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, content_sha FROM memories`)
	if err != nil {
		return nil, wrap("read memory index", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, sha string
		if err := rows.Scan(&name, &sha); err != nil {
			return nil, wrap("read memory index", err)
		}
		out[name] = sha
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("read memory index", err)
	}
	return out, nil
}

// DeleteMemory removes the index row named name.
func (s *Store) DeleteMemory(ctx context.Context, name string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM memories WHERE name = $1`, name); err != nil {
		return wrap("delete memory row "+name, err)
	}
	return nil
}

const memoryColumns = `name, description, type, source, created_at, updated_at, expires, pinned, state, content_sha`

type rowScanner interface{ Scan(dest ...any) error }

// scanMemory reads memoryColumns, then any extra columns, into a summary.
func scanMemory(row rowScanner, extra ...any) (memory.Summary, error) {
	var m memory.Summary
	var created, updated time.Time
	var expires *time.Time
	dest := append([]any{&m.Name, &m.Description, &m.Type, &m.Source, &created, &updated, &expires, &m.Pinned, &m.State, &m.ContentSHA}, extra...)
	if err := row.Scan(dest...); err != nil {
		return memory.Summary{}, err
	}
	m.Created, m.Updated = created.UTC().Format(time.RFC3339), updated.UTC().Format(time.RFC3339)
	if expires != nil {
		m.Expires = expires.Format("2006-01-02")
	}
	return m, nil
}

// ListMemories pages memory rows without their bodies, newest update first.
func (s *Store) ListMemories(ctx context.Context, q memory.ListQuery) ([]memory.Summary, error) {
	var after *time.Time
	if q.AfterName != "" {
		after = &q.AfterUpdated
	}
	rows, err := s.pool.Query(ctx, `SELECT `+memoryColumns+` FROM memories
        WHERE ($1 = '' OR state = $1) AND ($2 = '' OR type = $2)
          AND ($3::boolean IS NULL OR pinned = $3)
          AND ($4::timestamptz IS NULL OR updated_at < $4 OR (updated_at = $4 AND name > $5))
        ORDER BY updated_at DESC, name ASC
        LIMIT $6`, q.State, q.Type, q.Pinned, after, q.AfterName, q.Limit)
	if err != nil {
		return nil, wrap("list memories", err)
	}
	defer rows.Close()
	var out []memory.Summary
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, wrap("list memories", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list memories", err)
	}
	return out, nil
}

// GetMemory returns one memory's row with its body, screen verdict and
// archive reason, or memory.ErrNotFound.
func (s *Store) GetMemory(ctx context.Context, name string) (memory.Record, error) {
	var body, reason string
	var screen []byte
	m, err := scanMemory(s.pool.QueryRow(ctx, `SELECT `+memoryColumns+`, body, screen, archive_reason FROM memories WHERE name = $1`, name), &body, &screen, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return memory.Record{}, memory.ErrNotFound
	}
	if err != nil {
		return memory.Record{}, wrap("read memory "+name, err)
	}
	rec := memory.Record{File: m.File, ContentSHA: m.ContentSHA, Reason: reason}
	rec.Body = body
	if len(screen) > 0 {
		var res memory.ScreenResult
		if err := json.Unmarshal(screen, &res); err != nil {
			return memory.Record{}, wrap("read memory "+name, err)
		}
		rec.Screen = &res
	}
	return rec, nil
}

// PinnedMemories returns every pinned, active memory with its body.
func (s *Store) PinnedMemories(ctx context.Context) ([]memory.File, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+memoryColumns+`, body FROM memories WHERE pinned AND state = 'active'`)
	if err != nil {
		return nil, wrap("read pinned memories", err)
	}
	defer rows.Close()
	var out []memory.File
	for rows.Next() {
		var body string
		m, err := scanMemory(rows, &body)
		if err != nil {
			return nil, wrap("read pinned memories", err)
		}
		m.Body = body
		out = append(out, m.File)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("read pinned memories", err)
	}
	return out, nil
}
