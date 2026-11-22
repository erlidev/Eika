package store

import (
	"context"
	"time"

	"github.com/erlidev/eika/internal/memory"
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
	_, err = s.pool.Exec(ctx, `INSERT INTO memories
        (name, description, type, source, created_at, updated_at, expires, pinned, state, content_sha, body)
        VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8, $9, $10, $11)
        ON CONFLICT (name) DO UPDATE SET description = excluded.description, type = excluded.type,
            source = excluded.source, created_at = excluded.created_at, updated_at = excluded.updated_at,
            expires = excluded.expires, pinned = excluded.pinned, state = excluded.state,
            content_sha = excluded.content_sha, body = excluded.body`,
		r.Name, r.Description, r.Type, r.Source, created, updated, expires, r.Pinned, r.State, r.ContentSHA, r.Body)
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
