package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// SessionKind says who opened a session. It is what the session tree in the
// user interface draws a row as: a fork and a child agent are both shown
// under the session they came from, and each reads differently.
type SessionKind string

// The kinds a session can have.
const (
	// SessionUser is a session the user opened.
	SessionUser SessionKind = "user"
	// SessionFork is a session forked from another at one of its entries.
	SessionFork SessionKind = "fork"
	// SessionAgent is the session of a subagent a run spawned.
	SessionAgent SessionKind = "agent"
)

// Session is one tree of entries inside a workspace.
type Session struct {
	ID          string
	WorkspaceID string
	Title       string
	// Kind is who opened the session. An empty kind on input is SessionUser.
	Kind SessionKind
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
const sessionColumns = `id, workspace_id, title, kind, head_entry_id, parent_session_id, created_at, updated_at`

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
	if sess.Kind == "" {
		sess.Kind = SessionUser
	}
	const insert = `INSERT INTO sessions (id, workspace_id, title, kind, head_entry_id, parent_session_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + sessionColumns
	row := q.QueryRow(ctx, insert, sess.ID, sess.WorkspaceID, sess.Title, sess.Kind,
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
//
// withDescendants also returns the forks and child agents those sessions led
// to, wherever they live. A fork with a workspace and a subagent both run in
// a workspace of their own, so a listing of one workspace would otherwise
// leave them out of the tree they belong to. The rows come back flat and the
// caller hangs each one under its ParentSessionID.
func (s *Store) Sessions(ctx context.Context, workspaceID string, withDescendants bool) ([]Session, error) {
	// One query per case: a WHERE that is true for every row when the filter
	// is empty cannot use the workspace index.
	const (
		all = `SELECT ` + sessionColumns + ` FROM sessions ORDER BY created_at, id`
		one = `SELECT ` + sessionColumns + ` FROM sessions
			WHERE workspace_id = $1 ORDER BY created_at, id`
		// UNION, not UNION ALL: it dedupes, so a descendant that runs in the
		// same workspace is returned once and a parent pointer that somehow
		// came round on itself terminates instead of looping.
		tree = `WITH RECURSIVE reachable AS (
				SELECT ` + sessionColumns + ` FROM sessions WHERE workspace_id = $1
				UNION
				SELECT s.id, s.workspace_id, s.title, s.kind, s.head_entry_id,
					s.parent_session_id, s.created_at, s.updated_at
				FROM sessions s JOIN reachable r ON s.parent_session_id = r.id
			)
			SELECT ` + sessionColumns + ` FROM reachable ORDER BY created_at, id`
	)
	var (
		rows pgx.Rows
		err  error
	)
	switch {
	case workspaceID == "":
		rows, err = s.pool.Query(ctx, all)
	case withDescendants:
		rows, err = s.pool.Query(ctx, tree, workspaceID)
	default:
		rows, err = s.pool.Query(ctx, one, workspaceID)
	}
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
	err := row.Scan(&sess.ID, &sess.WorkspaceID, &sess.Title, &sess.Kind, &head, &parent,
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
