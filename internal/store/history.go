package store

import (
	"context"
	"encoding/json"
	"fmt"
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

// HistoryContext tracks the stored messages represented by a session path.
// Compacted is the span removed from the current context; FirstKept is the
// oldest message still present. Sequence numbers are local to the session.
type HistoryContext struct {
	FirstCompacted int64
	LastCompacted  int64
	FirstKept      int64
	Compactions    int
}

// HistoryState folds compaction records on a path. If keep is non-negative it
// also applies a pending compaction that retains that many messages.
func HistoryState(path []Entry, keep int) (HistoryContext, error) {
	var state HistoryContext
	var current []int64
	compact := func(n int) {
		n = min(max(n, 0), len(current))
		for _, seq := range current[:len(current)-n] {
			if seq == 0 {
				continue
			}
			if state.FirstCompacted == 0 || seq < state.FirstCompacted {
				state.FirstCompacted = seq
			}
			if seq > state.LastCompacted {
				state.LastCompacted = seq
			}
		}
		current = append([]int64{0}, current[len(current)-n:]...)
		state.Compactions++
	}
	for _, e := range path {
		switch e.Kind {
		case KindUser, KindAssistant, KindToolResult:
			current = append(current, e.Seq)
		case KindCompaction:
			var c struct {
				Kept int `json:"kept"`
			}
			if err := json.Unmarshal(e.Payload, &c); err != nil {
				return HistoryContext{}, fmt.Errorf("decode compaction entry %s: %w", e.ID, err)
			}
			compact(c.Kept)
		}
	}
	if keep >= 0 {
		compact(keep)
	}
	for _, seq := range current {
		if seq != 0 {
			state.FirstKept = seq
			break
		}
	}
	return state, nil
}

// BrowseHistory returns indexed entries before a sequence number, newest first.
// A zero before value returns no entries, because no compacted span exists.
func (s *Store) BrowseHistory(ctx context.Context, filter HistoryFilter, before int64) ([]Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM session_entries e WHERE e.session_id = $1
 AND $2::bigint > 0 AND e.seq < $2 AND e.kind IN ('user', 'assistant', 'tool_call', 'notice', 'compaction')
 AND ($3::timestamptz IS NULL OR e.created_at >= $3)
 AND ($4::timestamptz IS NULL OR e.created_at < $4)
 ORDER BY e.seq DESC LIMIT $5 OFFSET $6`
	return s.queryEntries(ctx, "browse history", q, filter.SessionID, before, filter.Since, filter.Until, filter.Limit, filter.Offset)
}

// HistoryTurn returns the turn containing entryID and one neighbouring turn on
// each side, limited to the entries closest to the hit. A turn starts at a
// user message and ends before the next one.
func (s *Store) HistoryTurn(ctx context.Context, entryID string, limit int) ([]Entry, error) {
	const q = `WITH anchor AS (SELECT session_id, seq FROM session_entries WHERE id = $1),
 bounds AS (SELECT a.session_id,
 COALESCE((SELECT seq FROM session_entries WHERE session_id = a.session_id AND kind = 'user' AND seq <=
 COALESCE((SELECT seq FROM session_entries WHERE session_id = a.session_id AND kind = 'user' AND seq <= a.seq ORDER BY seq DESC LIMIT 1), a.seq)
 ORDER BY seq DESC OFFSET 1 LIMIT 1), 0) AS first,
 COALESCE((SELECT seq FROM session_entries WHERE session_id = a.session_id AND kind = 'user' AND seq > a.seq ORDER BY seq OFFSET 1 LIMIT 1), 9223372036854775807) AS last
 FROM anchor a)
 SELECT ` + entryColumns + ` FROM (SELECT e.id, e.session_id, e.parent_id, e.seq, e.kind, e.payload, e.created_at, e.channel
 FROM session_entries e JOIN bounds b ON e.session_id = b.session_id CROSS JOIN anchor a
 WHERE e.seq >= b.first AND e.seq < b.last AND e.kind IN ('user','assistant','tool_call','notice','compaction')
 ORDER BY abs(e.seq - a.seq), e.seq LIMIT $2) closest ORDER BY seq`
	if _, err := s.Entry(ctx, entryID); err != nil {
		return nil, err
	}
	return s.queryEntries(ctx, "read history turn", q, entryID, limit)
}

// EntryBySequence finds an entry by its session-local sequence number.
func (s *Store) EntryBySequence(ctx context.Context, sessionID string, seq int64) (Entry, error) {
	e, err := scanEntry(s.pool.QueryRow(ctx, `SELECT `+entryColumns+` FROM session_entries WHERE session_id = $1 AND seq = $2`, sessionID, seq))
	if err != nil {
		return Entry{}, wrap("read history entry", err)
	}
	return e, nil
}
