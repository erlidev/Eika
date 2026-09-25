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

// Session is one tree of entries, inside a workspace or, for a chat, in none.
type Session struct {
	ID string
	// WorkspaceID is the workspace the session's runs act in, empty for a
	// chat.
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
	// Tools names the tools the session's runs may offer the model. Nil is
	// every tool the session can run; an empty, non-nil slice is none.
	Tools []string
	// ProfileID is the profile the session's runs start from, empty for
	// whichever profile is the default when a run starts.
	ProfileID string
	// Overrides are the profile settings the session sets for itself; what
	// they leave unset comes from the profile.
	Overrides ProfileSettings
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Chat reports whether the session is a chat: one with no workspace, whose
// runs offer the model only the tools that need none.
func (s Session) Chat() bool { return s.WorkspaceID == "" }

// sessionColumns is the column list every session query selects, in the order
// scanSession reads them.
const sessionColumns = `id, workspace_id, title, kind, head_entry_id, parent_session_id, tools,
	profile_id, overrides, created_at, updated_at`

// CreateSession inserts sess and returns it with the fields the database
// assigned. An empty ID gets a fresh one; an unknown profile is ErrNotFound.
func (s *Store) CreateSession(ctx context.Context, sess Session) (Session, error) {
	out, err := createSession(ctx, s.pool, sess)
	if err != nil {
		return Session{}, wrapMissing("create session", err)
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
	overrides, err := overridesJSON(sess.Overrides)
	if err != nil {
		return Session{}, err
	}
	const insert = `INSERT INTO sessions (id, workspace_id, title, kind, head_entry_id, parent_session_id, tools,
			profile_id, overrides)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + sessionColumns
	row := q.QueryRow(ctx, insert, sess.ID, nullable(sess.WorkspaceID), sess.Title, sess.Kind,
		nullable(sess.HeadEntryID), nullable(sess.ParentSessionID), sess.Tools,
		nullable(sess.ProfileID), overrides)
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
// workspaceID returns every session, chats included.
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
					s.parent_session_id, s.tools, s.profile_id, s.overrides, s.created_at, s.updated_at
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
	return collectSessions("list sessions", rows)
}

// Chats returns every session with no workspace, oldest first. A fork of a
// chat is a chat, so the list holds the forks as well; the caller hangs each
// under its ParentSessionID.
func (s *Store) Chats(ctx context.Context) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sessionColumns+` FROM sessions
		WHERE workspace_id IS NULL ORDER BY created_at, id`)
	if err != nil {
		return nil, wrap("list chats", err)
	}
	return collectSessions("list chats", rows)
}

// collectSessions reads every row of a session query and closes it.
func collectSessions(op string, rows pgx.Rows) ([]Session, error) {
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, wrap(op, err)
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(op, err)
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

// SetSessionTools chooses the tools a session's runs may offer the model. Nil
// restores every tool the session can run; an empty, non-nil slice is none.
func (s *Store) SetSessionTools(ctx context.Context, id string, tools []string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE sessions SET tools = $2, updated_at = now() WHERE id = $1`, id, tools)
	if err != nil {
		return wrap("set session tools "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("set session tools "+id, pgx.ErrNoRows)
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
		sess      Session
		workspace *string
		head      *string
		parent    *string
		profile   *string
		overrides []byte
	)
	err := row.Scan(&sess.ID, &workspace, &sess.Title, &sess.Kind, &head, &parent, &sess.Tools,
		&profile, &overrides, &sess.CreatedAt, &sess.UpdatedAt)
	if err != nil {
		return Session{}, err
	}
	if sess.Overrides, err = readOverrides(overrides); err != nil {
		return Session{}, err
	}
	sess.WorkspaceID = text(workspace)
	sess.HeadEntryID = text(head)
	sess.ParentSessionID = text(parent)
	sess.ProfileID = text(profile)
	sess.CreatedAt = sess.CreatedAt.UTC()
	sess.UpdatedAt = sess.UpdatedAt.UTC()
	return sess, nil
}
