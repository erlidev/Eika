package store

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// EntryKind names what one session entry holds. The kinds are the vocabulary
// the whole system agrees on, in the same way internal/event fixes the event
// names: internal/session writes user, assistant, and tool_result entries
// from provider messages, system entries for prompt-level notes, and event
// entries for questions, subagent lifecycle, and later compaction.
type EntryKind string

// The kinds an entry can have.
const (
	// KindUser is a message from the user.
	KindUser EntryKind = "user"
	// KindAssistant is a model response: text, tool calls, or both.
	KindAssistant EntryKind = "assistant"
	// KindToolCall is one tool call recorded on its own.
	KindToolCall EntryKind = "tool_call"
	// KindToolResult is the result of one tool call.
	KindToolResult EntryKind = "tool_result"
	// KindSystem is a note that belongs to the conversation's framing rather
	// than to a turn.
	KindSystem EntryKind = "system"
	// KindEvent is something that happened around the session: a question, a
	// subagent starting or finishing, a compaction.
	KindEvent EntryKind = "event"
)

// Entry is one node of a session tree. Entries form a tree through ParentID
// and are ordered within a session by Seq.
type Entry struct {
	ID        string
	SessionID string
	// ParentID is the entry this one was appended after, empty at the root.
	ParentID string
	// Seq orders the entries of a session by the time they were written,
	// across every branch.
	Seq  int64
	Kind EntryKind
	// Payload is the entry's content. For an entry that carries a message it
	// is the provider.Message JSON; internal/session owns the shapes. It is
	// stored as jsonb, which keeps the document and not its spelling: the
	// bytes that come back are equal as JSON to the bytes that went in, but
	// key order, whitespace, and number formatting are the database's.
	Payload json.RawMessage
	// Commit is the workspace HEAD commit the entry was produced at, empty
	// when the caller did not record one. Forking with a workspace clones at
	// this commit.
	Commit    string
	CreatedAt time.Time
}

// entryColumns is the column list every entry query selects, in the order
// scanEntry reads them.
const entryColumns = `id, session_id, parent_id, seq, kind, payload, commit_sha, created_at`

// AppendEntry appends e to the session's head and moves the head to it, in
// one transaction. ID, SessionID, ParentID, Seq, and CreatedAt are assigned
// here and ignored on input; Kind, Payload, and Commit are the caller's.
func (s *Store) AppendEntry(ctx context.Context, sessionID string, e Entry) (Entry, error) {
	var out Entry
	err := s.tx(ctx, func(q querier) error {
		head, err := lockSessionHead(ctx, q, sessionID)
		if err != nil {
			return err
		}
		if out, err = insertEntry(ctx, q, sessionID, head, e); err != nil {
			return err
		}
		return setHead(ctx, q, sessionID, out.ID)
	})
	if err != nil {
		return Entry{}, wrap("append entry to session "+sessionID, err)
	}
	return out, nil
}

// SetSessionHead points a session's head at one of its own entries, which is
// how a branch starts: the next append hangs off that entry instead of the
// one that was last written. An empty entryID clears the head, so the session
// starts again from its root.
func (s *Store) SetSessionHead(ctx context.Context, sessionID, entryID string) error {
	// The entry check is part of the update, so that no head can be set from
	// a row that another connection deleted in between.
	const q = `UPDATE sessions SET head_entry_id = $2, updated_at = now()
		WHERE id = $1 AND ($2::text IS NULL OR EXISTS (
			SELECT 1 FROM session_entries WHERE id = $2 AND session_id = $1))`
	tag, err := s.pool.Exec(ctx, q, sessionID, nullable(entryID))
	if err != nil {
		return wrap("set head of session "+sessionID, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("set head of session "+sessionID, pgx.ErrNoRows)
	}
	return nil
}

// Entry returns one entry by id.
func (s *Store) Entry(ctx context.Context, id string) (Entry, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+entryColumns+` FROM session_entries WHERE id = $1`, id)
	e, err := scanEntry(row)
	if err != nil {
		return Entry{}, wrap("read entry "+id, err)
	}
	return e, nil
}

// Entries returns every entry of a session in write order, across branches.
func (s *Store) Entries(ctx context.Context, sessionID string) ([]Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM session_entries WHERE session_id = $1 ORDER BY seq`
	return s.queryEntries(ctx, "list entries of session "+sessionID, q, sessionID)
}

// ChildEntries returns the entries whose parent is entryID, oldest first. An
// empty entryID returns the session's roots.
func (s *Store) ChildEntries(ctx context.Context, sessionID, entryID string) ([]Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM session_entries
		WHERE session_id = $1 AND parent_id IS NOT DISTINCT FROM $2
		ORDER BY seq`
	return s.queryEntries(ctx, "list children of entry "+entryID, q, sessionID, nullable(entryID))
}

// EntryPath returns the entries from the session's root down to entryID,
// oldest first. It is the branch of the tree that entry sits on.
func (s *Store) EntryPath(ctx context.Context, sessionID, entryID string) ([]Entry, error) {
	if _, err := s.entryOfSession(ctx, sessionID, entryID); err != nil {
		return nil, err
	}
	return s.queryEntries(ctx, "read path to entry "+entryID, pathQuery, entryID)
}

// SessionPath returns the entries from the session's root down to its head.
// It is what becomes the provider conversation. A session with no head has an
// empty path.
func (s *Store) SessionPath(ctx context.Context, sessionID string) ([]Entry, error) {
	sess, err := s.Session(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.HeadEntryID == "" {
		return nil, nil
	}
	return s.queryEntries(ctx, "read path of session "+sessionID, pathQuery, sess.HeadEntryID)
}

// pathQuery walks parent pointers up from one entry and returns the branch it
// sits on, oldest first.
const pathQuery = `WITH RECURSIVE up AS (
		SELECT ` + entryColumns + ` FROM session_entries WHERE id = $1
		UNION ALL
		SELECT e.id, e.session_id, e.parent_id, e.seq, e.kind, e.payload, e.commit_sha, e.created_at
		FROM session_entries e JOIN up ON e.id = up.parent_id
	)
	SELECT ` + entryColumns + ` FROM up ORDER BY seq`

// ForkOptions configure ForkSession.
type ForkOptions struct {
	// Title of the new session. Empty copies the source session's title.
	Title string
	// WorkspaceID the fork runs in. Empty keeps the source workspace, which
	// is a fork of the conversation only, and keeps a chat's fork a chat;
	// fork-with-workspace passes a new workspace cloned at the fork entry's
	// commit.
	WorkspaceID string
}

// ForkSession creates a session that starts as a copy of the path from the
// source session's root down to entryID, with its head on the copy of that
// entry. The copies are rows of their own: a fork shares no entry with the
// session it came from, so editing or continuing either one cannot disturb
// the other, and deleting one cannot orphan the other. The fork keeps the
// source's choice of tools, its profile, and its overrides.
func (s *Store) ForkSession(ctx context.Context, sessionID, entryID string, opts ForkOptions) (Session, error) {
	var out Session
	err := s.tx(ctx, func(q querier) error {
		src, err := scanSession(q.QueryRow(ctx,
			`SELECT `+sessionColumns+` FROM sessions WHERE id = $1`, sessionID))
		if err != nil {
			return wrap("fork session "+sessionID, err)
		}
		path, err := entriesFrom(ctx, q, pathQuery, entryID)
		if err != nil {
			return wrap("fork session "+sessionID, err)
		}
		if len(path) == 0 || path[len(path)-1].SessionID != sessionID {
			return fmt.Errorf("fork session %s at entry %s: %w", sessionID, entryID, ErrNotFound)
		}

		fork := Session{
			WorkspaceID:     cmp.Or(opts.WorkspaceID, src.WorkspaceID),
			Title:           cmp.Or(opts.Title, src.Title),
			Kind:            SessionFork,
			ParentSessionID: sessionID,
			Tools:           src.Tools,
			ProfileID:       src.ProfileID,
			Overrides:       src.Overrides,
		}
		if fork, err = createSession(ctx, q, fork); err != nil {
			return wrap("fork session "+sessionID, err)
		}
		parent := ""
		for _, e := range path {
			copied, err := insertEntry(ctx, q, fork.ID, parent, e)
			if err != nil {
				return wrap("fork session "+sessionID, err)
			}
			parent = copied.ID
		}
		if err := setHead(ctx, q, fork.ID, parent); err != nil {
			return wrap("fork session "+sessionID, err)
		}
		fork.HeadEntryID = parent
		out = fork
		return nil
	})
	if err != nil {
		return Session{}, err
	}
	return out, nil
}

// insertEntry writes one entry under parent, giving it the next sequence
// number of its session.
//
// The caller must hold the session row lock that lockSessionHead takes:
// max(seq) + 1 is only unique while one writer at a time reads it. Two
// concurrent appends without the lock would pick the same number and one of
// them would lose the (session_id, seq) uniqueness check.
func insertEntry(ctx context.Context, q querier, sessionID, parentID string, e Entry) (Entry, error) {
	const insert = `INSERT INTO session_entries (id, session_id, parent_id, seq, kind, payload, commit_sha)
		VALUES ($1, $2, $3,
			(SELECT coalesce(max(seq), 0) + 1 FROM session_entries WHERE session_id = $2),
			$4, $5, $6)
		RETURNING ` + entryColumns
	payload := e.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	row := q.QueryRow(ctx, insert, NewID(), sessionID, nullable(parentID), e.Kind, []byte(payload), nullable(e.Commit))
	return scanEntry(row)
}

// lockSessionHead reads a session's head and locks the row until the
// transaction ends, so that two appends cannot pick the same parent or the
// same sequence number.
func lockSessionHead(ctx context.Context, q querier, sessionID string) (string, error) {
	var head *string
	err := q.QueryRow(ctx, `SELECT head_entry_id FROM sessions WHERE id = $1 FOR UPDATE`, sessionID).Scan(&head)
	if err != nil {
		return "", err
	}
	return text(head), nil
}

// setHead points a session at an entry without checking that the entry is
// one of its own; callers that take an id from outside check first.
func setHead(ctx context.Context, q querier, sessionID, entryID string) error {
	tag, err := q.Exec(ctx, `UPDATE sessions SET head_entry_id = $2, updated_at = now() WHERE id = $1`,
		sessionID, nullable(entryID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// entryOfSession returns an entry only when it belongs to the named session.
func (s *Store) entryOfSession(ctx context.Context, sessionID, entryID string) (Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM session_entries WHERE id = $1 AND session_id = $2`
	e, err := scanEntry(s.pool.QueryRow(ctx, q, entryID, sessionID))
	if err != nil {
		return Entry{}, wrap("read entry "+entryID+" of session "+sessionID, err)
	}
	return e, nil
}

// queryEntries runs an entry query and describes it if it fails.
func (s *Store) queryEntries(ctx context.Context, op, query string, args ...any) ([]Entry, error) {
	entries, err := entriesFrom(ctx, s.pool, query, args...)
	if err != nil {
		return nil, wrap(op, err)
	}
	return entries, nil
}

// entriesFrom runs an entry query on any querier.
func entriesFrom(ctx context.Context, q querier, query string, args ...any) ([]Entry, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanEntry reads one entry row.
func scanEntry(row pgx.Row) (Entry, error) {
	var (
		e       Entry
		parent  *string
		commit  *string
		payload []byte
	)
	err := row.Scan(&e.ID, &e.SessionID, &parent, &e.Seq, &e.Kind, &payload, &commit, &e.CreatedAt)
	if err != nil {
		return Entry{}, err
	}
	e.ParentID = text(parent)
	e.Commit = text(commit)
	e.Payload = json.RawMessage(payload)
	e.CreatedAt = e.CreatedAt.UTC()
	return e, nil
}
