package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// SessionKind identifies the role of a conversation.
type SessionKind string

// The kinds a session can have.
const (
	// SessionUser is a session the user opened.
	SessionUser SessionKind = "user"
	// SessionHome is the owner's main conversation.
	SessionHome SessionKind = "home"
	// SessionSubagent is a named delegated conversation.
	SessionSubagent SessionKind = "subagent"
	// SessionRoutine is a scheduled conversation.
	SessionRoutine SessionKind = "routine"
)

// SessionState is the lifecycle state set by code.
type SessionState string

// The lifecycle states of a session.
const (
	// SessionIdle has no active run.
	SessionIdle SessionState = "idle"
	// SessionRunning has an active run.
	SessionRunning SessionState = "running"
	// SessionWaiting has a run waiting for input.
	SessionWaiting SessionState = "waiting"
	// SessionFailed has a failed run.
	SessionFailed SessionState = "failed"
	// SessionCancelled has a cancelled run.
	SessionCancelled SessionState = "cancelled"
)

// LocalOwner is the storage identity of the local user.
const LocalOwner = "local"

// Session is one tree of entries, inside a computer or, for a chat, in none.
type Session struct {
	ID              string
	Owner           string
	Agent           string
	Name            string
	ProjectID       string
	ParentSessionID string
	WaitingReason   string
	State           SessionState
	Brief           string
	Result          string
	LastActiveAt    time.Time
	// ComputerID is the computer the session's runs act in, empty for a
	// chat.
	ComputerID string
	Title      string
	// Untitled reports that Title is a placeholder, which the first run
	// replaces with a title made from the first message.
	Untitled bool
	// Kind identifies the conversation. An empty kind on input is SessionUser.
	Kind SessionKind
	// HeadEntryID is the entry a run continues from, empty in a session that
	// has no entries yet.
	HeadEntryID string
	// Tools names the tools the session's runs may offer the model. Nil is
	// every tool the session can run; an empty, non-nil slice is none.
	Tools []string
	// ProfileID is the profile the session's runs start from, empty for
	// whichever profile is the default when a run starts.
	ProfileID string
	// Overrides are the profile settings the session sets for itself; what
	// they leave unset comes from the profile.
	Overrides ProfileSettings
	// Pinned sessions are listed before the others.
	Pinned bool
	// Archived sessions are set aside under a heading of their own. It is
	// filing only: an archived session runs like any other.
	Archived  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Chat reports whether the session is a chat: one with no computer, whose
// runs offer the model only the tools that need none.
func (s Session) Chat() bool { return s.ComputerID == "" }

// sessionColumns is the column list every session query selects, in the order
// scanSession reads them.
const sessionColumns = `id, computer_id, title, untitled, kind, head_entry_id, tools,
	profile_id, overrides, pinned, archived, created_at, updated_at,
    owner, agent, name, project_id, parent_session_id, state, brief, result, last_active_at, waiting_reason`

// CreateSession inserts sess and returns it with the fields the database
// assigned. An empty ID gets a fresh one; an unknown profile is ErrNotFound.
func (s *Store) CreateSession(ctx context.Context, sess Session) (Session, error) {
	if sess.ID == "" {
		sess.ID = NewID()
	}
	if sess.Kind == "" {
		sess.Kind = SessionUser
	}
	if sess.Owner == "" {
		sess.Owner = LocalOwner
	}
	if sess.State == "" {
		sess.State = SessionIdle
	}
	var active *time.Time
	if !sess.LastActiveAt.IsZero() {
		value := sess.LastActiveAt.UTC()
		active = &value
	}
	overrides, err := overridesJSON(sess.Overrides)
	if err != nil {
		return Session{}, wrapMissing("create session", err)
	}
	const insert = `INSERT INTO sessions (id, computer_id, title, untitled, kind, head_entry_id,
			tools, profile_id, overrides, pinned, archived, owner, agent, name, project_id, parent_session_id, state, brief, result, last_active_at, waiting_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, COALESCE($20, now()), $21)
		RETURNING ` + sessionColumns
	row := s.pool.QueryRow(ctx, insert, sess.ID, nullable(sess.ComputerID), sess.Title, sess.Untitled, sess.Kind,
		nullable(sess.HeadEntryID), sess.Tools,
		nullable(sess.ProfileID), overrides, sess.Pinned, sess.Archived, sess.Owner, sess.Agent, sess.Name, nullable(sess.ProjectID), nullable(sess.ParentSessionID), sess.State, sess.Brief, sess.Result, active, sess.WaitingReason)
	out, err := scanSession(row)
	if err != nil {
		return Session{}, wrapMissing("create session", err)
	}
	return out, nil
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

// Sessions returns the sessions of one computer, oldest first. An empty
// computerID returns every session, chats included.
func (s *Store) Sessions(ctx context.Context, computerID string) ([]Session, error) {
	// One query per case: a WHERE that is true for every row when the filter
	// is empty cannot use the computer index.
	const (
		all = `SELECT ` + sessionColumns + ` FROM sessions ORDER BY created_at, id`
		one = `SELECT ` + sessionColumns + ` FROM sessions
			WHERE computer_id = $1 ORDER BY created_at, id`
	)
	var (
		rows pgx.Rows
		err  error
	)
	if computerID == "" {
		rows, err = s.pool.Query(ctx, all)
	} else {
		rows, err = s.pool.Query(ctx, one, computerID)
	}
	if err != nil {
		return nil, wrap("list sessions", err)
	}
	return collectSessions("list sessions", rows)
}

// Chats returns non-home sessions with no computer, oldest first.
func (s *Store) Chats(ctx context.Context) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sessionColumns+` FROM sessions
		WHERE computer_id IS NULL AND kind <> 'home' ORDER BY created_at, id`)
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

// SessionChanges are the edits a user makes to a session's title and
// filing. A nil field is left as it is.
type SessionChanges struct {
	// Title renames the session. A title set this way is final: the session
	// is no longer untitled.
	Title    *string
	Pinned   *bool
	Archived *bool
}

// UpdateSession applies changes to a session and returns it as it now stands.
func (s *Store) UpdateSession(ctx context.Context, id string, changes SessionChanges) (Session, error) {
	const q = `UPDATE sessions
		SET title = COALESCE($2, title),
		    untitled = untitled AND $2::text IS NULL,
		    pinned = COALESCE($3, pinned),
		    archived = COALESCE($4, archived),
		    updated_at = now()
		WHERE id = $1
		RETURNING ` + sessionColumns
	row := s.pool.QueryRow(ctx, q, id, changes.Title, changes.Pinned, changes.Archived)
	sess, err := scanSession(row)
	if err != nil {
		return Session{}, wrap("update session "+id, err)
	}
	return sess, nil
}

// TitleUntitledSession gives an untitled session its title and reports
// whether it did. A session that was titled meanwhile keeps its title, so a
// title made in the background never replaces one someone chose.
func (s *Store) TitleUntitledSession(ctx context.Context, id, title string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE sessions SET title = $2, untitled = false, updated_at = now()
		WHERE id = $1 AND untitled`, id, title)
	if err != nil {
		return false, wrap("title session "+id, err)
	}
	return tag.RowsAffected() == 1, nil
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
		sess            Session
		computer        *string
		head            *string
		profile         *string
		project, parent *string
		overrides       []byte
	)
	err := row.Scan(&sess.ID, &computer, &sess.Title, &sess.Untitled, &sess.Kind, &head, &sess.Tools,
		&profile, &overrides, &sess.Pinned, &sess.Archived, &sess.CreatedAt, &sess.UpdatedAt, &sess.Owner, &sess.Agent, &sess.Name, &project, &parent, &sess.State, &sess.Brief, &sess.Result, &sess.LastActiveAt, &sess.WaitingReason)
	if err != nil {
		return Session{}, err
	}
	if sess.Overrides, err = readOverrides(overrides); err != nil {
		return Session{}, err
	}
	sess.ProjectID = text(project)
	sess.ParentSessionID = text(parent)
	sess.LastActiveAt = sess.LastActiveAt.UTC()
	sess.ComputerID = text(computer)
	sess.HeadEntryID = text(head)
	sess.ProfileID = text(profile)
	sess.CreatedAt = sess.CreatedAt.UTC()
	sess.UpdatedAt = sess.UpdatedAt.UTC()
	return sess, nil
}

// HomeSession returns the home conversation identified by owner and name.
func (s *Store) HomeSession(ctx context.Context, owner, name string) (Session, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions
		WHERE kind = 'home' AND owner = $1 AND name = $2`, owner, name)
	sess, err := scanSession(row)
	if err != nil {
		return Session{}, wrap("read home session", err)
	}
	return sess, nil
}

// EnsureHomeSession creates or returns the local home conversation.
func (s *Store) EnsureHomeSession(ctx context.Context) (Session, error) {
	const q = `INSERT INTO sessions (id, title, kind, owner, agent, name)
 VALUES ($1, 'Home', 'home', $2, 'eika', 'home')
 ON CONFLICT (owner, name) WHERE kind = 'home' DO UPDATE SET name = EXCLUDED.name
 RETURNING ` + sessionColumns
	sess, err := scanSession(s.pool.QueryRow(ctx, q, NewID(), LocalOwner))
	if err != nil {
		return Session{}, wrap("ensure home session", err)
	}
	return sess, nil
}
