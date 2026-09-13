package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Session is one tree of entries inside a workspace.
type Session struct {
	ID          string
	WorkspaceID string
	Title       string
	// HeadEntryID is the entry a run continues from, empty in a session that
	// has no entries yet.
	HeadEntryID string
	// ParentSessionID is the session this one was forked from, empty for a
	// session the user started.
	ParentSessionID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// sessionColumns is the column list every session query selects, in the order
// scanSession reads them.
const sessionColumns = `id, workspace_id, title, head_entry_id, parent_session_id, created_at, updated_at`

// CreateSession inserts sess and returns it with the fields the database
// assigned. An empty ID gets a fresh one.
func (s *Store) CreateSession(ctx context.Context, sess Session) (Session, error) {
	out, err := createSession(ctx, s.pool, sess)
	if err != nil {
		return Session{}, wrap("create session", err)
	}
	return out, nil
}

// createSession is the insert both CreateSession and ForkSession run.
func createSession(ctx context.Context, q querier, sess Session) (Session, error) {
	if sess.ID == "" {
		sess.ID = NewID()
	}
	const insert = `INSERT INTO sessions (id, workspace_id, title, head_entry_id, parent_session_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + sessionColumns
	row := q.QueryRow(ctx, insert, sess.ID, sess.WorkspaceID, sess.Title,
		nullable(sess.HeadEntryID), nullable(sess.ParentSessionID))
	return scanSession(row)
}

// Session returns the session with the given id.
func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = $1`, id)
	sess, err := scanSession(row)
	if err != nil {
		return Session{}, wrap("read session "+id, err)
	}
	return sess, nil
}

// Sessions returns the sessions of one workspace, oldest first. An empty
// workspaceID returns every session.
func (s *Store) Sessions(ctx context.Context, workspaceID string) ([]Session, error) {
	const q = `SELECT ` + sessionColumns + ` FROM sessions
		WHERE $1 = '' OR workspace_id = $1
		ORDER BY created_at, id`
	rows, err := s.pool.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, wrap("list sessions", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, wrap("list sessions", err)
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list sessions", err)
	}
	return out, nil
}

// SetSessionTitle renames a session.
func (s *Store) SetSessionTitle(ctx context.Context, id, title string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE sessions SET title = $2, updated_at = now() WHERE id = $1`, id, title)
	if err != nil {
		return wrap("set session title "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("set session title "+id, pgx.ErrNoRows)
	}
	return nil
}

// DeleteSession removes a session and its entries.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return wrap("delete session "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("delete session "+id, pgx.ErrNoRows)
	}
	return nil
}

// scanSession reads one session row.
func scanSession(row pgx.Row) (Session, error) {
	var (
		sess   Session
		head   *string
		parent *string
	)
	err := row.Scan(&sess.ID, &sess.WorkspaceID, &sess.Title, &head, &parent,
		&sess.CreatedAt, &sess.UpdatedAt)
	if err != nil {
		return Session{}, err
	}
	sess.HeadEntryID = text(head)
	sess.ParentSessionID = text(parent)
	sess.CreatedAt = sess.CreatedAt.UTC()
	sess.UpdatedAt = sess.UpdatedAt.UTC()
	return sess, nil
}
