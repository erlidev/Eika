package store

import "context"

// Subagent returns a child by its parent and exact name.
func (s *Store) Subagent(ctx context.Context, parent, name string) (Session, error) {
	sess, err := scanSession(s.pool.QueryRow(ctx, `SELECT `+sessionColumns+`
        FROM sessions WHERE kind = 'subagent' AND parent_session_id = $1 AND name = $2`, parent, name))
	if err != nil {
		return Session{}, wrap("read subagent", err)
	}
	return sess, nil
}

// Subagents returns the parent's children in creation order.
func (s *Store) Subagents(ctx context.Context, parent string) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sessionColumns+` FROM sessions
        WHERE kind = 'subagent' AND parent_session_id = $1 ORDER BY created_at, id`, parent)
	if err != nil {
		return nil, wrap("list subagents", err)
	}
	return collectSessions("list subagents", rows)
}

// SetSubagentState records a code-owned state and its waiting reason.
// An unchanged state and reason retain their activity timestamps.
func (s *Store) SetSubagentState(ctx context.Context, id string, state SessionState, reason string) (Session, error) {
	sess, err := scanSession(s.pool.QueryRow(ctx, `UPDATE sessions SET
        state = $2, waiting_reason = $3,
        last_active_at = CASE WHEN (state, waiting_reason) IS DISTINCT FROM ($2::text, $3::text) THEN now() ELSE last_active_at END,
        updated_at = CASE WHEN (state, waiting_reason) IS DISTINCT FROM ($2::text, $3::text) THEN now() ELSE updated_at END
        WHERE id = $1 AND kind = 'subagent' RETURNING `+sessionColumns, id, state, reason))
	if err != nil {
		return Session{}, wrap("set subagent state", err)
	}
	return sess, nil
}
