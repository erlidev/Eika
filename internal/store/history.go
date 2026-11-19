package store

import (
	"context"
	"time"
)

// HistoryFilter selects entries by session, project and creation time. Empty
// IDs and nil dates leave the corresponding scope open. Until is exclusive.
type HistoryFilter struct {
	SessionID string
	ProjectID string
	Since     *time.Time
	Until     *time.Time
	Limit     int
	Offset    int
}

// HistoryHit is a matching entry and its full-text relevance score.
type HistoryHit struct {
	Entry Entry
	Rank  float32
}

// SearchHistory returns matches in relevance order, newest first on ties.
// Query uses PostgreSQL's plain text search syntax. Limit must be positive.
func (s *Store) SearchHistory(ctx context.Context, query string, filter HistoryFilter) ([]HistoryHit, error) {
	const q = `SELECT e.id, e.session_id, e.parent_id, e.seq, e.kind, e.payload, e.created_at, e.channel,
 ts_rank_cd(e.tsv, plainto_tsquery('english', $1)) AS rank
 FROM session_entries e JOIN sessions s ON s.id = e.session_id
 WHERE e.tsv @@ plainto_tsquery('english', $1)
 AND ($2 = '' OR e.session_id = $2) AND ($3 = '' OR s.project_id = $3)
 AND ($4::timestamptz IS NULL OR e.created_at >= $4)
 AND ($5::timestamptz IS NULL OR e.created_at < $5)
 ORDER BY rank DESC, e.created_at DESC, e.id DESC LIMIT $6 OFFSET $7`
	rows, err := s.pool.Query(ctx, q, query, filter.SessionID, filter.ProjectID, filter.Since, filter.Until, filter.Limit, filter.Offset)
	if err != nil {
		return nil, wrap("search history", err)
	}
	defer rows.Close()
	var hits []HistoryHit
	for rows.Next() {
		var hit HistoryHit
		var parent *string
		var payload []byte
		e := &hit.Entry
		if err := rows.Scan(&e.ID, &e.SessionID, &parent, &e.Seq, &e.Kind, &payload, &e.CreatedAt, &e.Channel, &hit.Rank); err != nil {
			return nil, wrap("search history", err)
		}
		e.ParentID = text(parent)
		e.Payload = payload
		e.CreatedAt = e.CreatedAt.UTC()
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("search history", err)
	}
	return hits, nil
}

// HistoryWindow returns entries around an entry in the same session, in write
// order. Before and after are the maximum counts on each side of the entry.
func (s *Store) HistoryWindow(ctx context.Context, entryID string, before, after int) ([]Entry, error) {
	const q = `WITH anchor AS (SELECT session_id, seq FROM session_entries WHERE id = $1),
 earlier AS (SELECT e.* FROM session_entries e, anchor a WHERE e.session_id = a.session_id AND e.seq < a.seq ORDER BY e.seq DESC LIMIT $2),
 later AS (SELECT e.* FROM session_entries e, anchor a WHERE e.session_id = a.session_id AND e.seq > a.seq ORDER BY e.seq LIMIT $3)
 SELECT ` + entryColumns + ` FROM (SELECT * FROM earlier UNION ALL SELECT e.* FROM session_entries e WHERE e.id = $1 UNION ALL SELECT * FROM later) window_entries ORDER BY seq`
	if _, err := s.Entry(ctx, entryID); err != nil {
		return nil, err
	}
	return s.queryEntries(ctx, "read history window", q, entryID, before, after)
}
