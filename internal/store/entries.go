package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// EntryKind names what one session entry holds. The kinds are the vocabulary
// the whole system agrees on, in the same way internal/event fixes the event
// names: internal/session writes user, assistant, and tool_result entries
// from provider messages and compaction entries for summaries.
type EntryKind string

// The kinds an entry can have.
const (
	// KindUser is a message from the user.
	KindUser EntryKind = "user"
	// KindAssistant is a model response: text, tool calls, or both.
	KindAssistant EntryKind = "assistant"
	// KindToolResult is the result of one tool call.
	KindToolResult EntryKind = "tool_result"
	// KindCompaction is a summary that replaces the conversation before it,
	// except its newest messages, from then on.
	KindCompaction EntryKind = "compaction"
	// KindPreamble is a stored context snapshot.
	KindPreamble EntryKind = "preamble"
	// KindNotice is harness text for the user and a later context block.
	KindNotice EntryKind = "notice"
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
	// Channel names the source or destination of a home message, empty if unset.
	Channel string
	// RunID identifies the agent turn that wrote this entry, if known.
	RunID string
	// Payload is the entry's content. For an entry that carries a message it
	// is the provider.Message JSON; internal/session owns the shapes. It is
	// stored as json, which preserves its exact spelling.
	Payload   json.RawMessage
	CreatedAt time.Time
}

// entryColumns is the column list every entry query selects, in the order
// scanEntry reads them.
const entryColumns = `id, session_id, parent_id, seq, kind, payload, created_at, channel, run_id`

// AppendEntry appends e to the session's head and moves the head to it, in
// one transaction. SessionID, ParentID, Seq, and CreatedAt are assigned here
// and ignored on input, and so is ID, which gets a fresh value; Kind, Payload
// Channel, and RunID are the caller's.
func (s *Store) AppendEntry(ctx context.Context, sessionID string, e Entry) (Entry, error) {
	e.ID = ""
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

// AppendEntryOnce appends e with the id e.ID gives it, unless an entry with
// that id exists: then it returns that entry and false, and changes nothing.
// A write retried after its response was lost thus adds one entry, not two.
func (s *Store) AppendEntryOnce(ctx context.Context, sessionID string, e Entry) (Entry, bool, error) {
	if e.ID == "" {
		return Entry{}, false, wrap("append entry once to session "+sessionID, errors.New("the entry has no id"))
	}
	var out Entry
	added := false
	err := s.tx(ctx, func(q querier) error {
		head, err := lockSessionHead(ctx, q, sessionID)
		if err != nil {
			return err
		}
		out, err = scanEntry(q.QueryRow(ctx, `SELECT `+entryColumns+` FROM session_entries WHERE id = $1`, e.ID))
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if out, err = insertEntry(ctx, q, sessionID, head, e); err != nil {
			return err
		}
		added = true
		return setHead(ctx, q, sessionID, out.ID)
	})
	if err != nil {
		return Entry{}, false, wrap("append entry once to session "+sessionID, err)
	}
	if out.SessionID != sessionID {
		return Entry{}, false, wrap("append entry once to session "+sessionID, ErrConflict)
	}
	return out, added, nil
}

// AppendEntries appends entries under one lock and commits them together.
func (s *Store) AppendEntries(ctx context.Context, sessionID string, entries ...Entry) error {
	return s.tx(ctx, func(q querier) error {
		head, err := lockSessionHead(ctx, q, sessionID)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			entry.ID = ""
			added, err := insertEntry(ctx, q, sessionID, head, entry)
			if err != nil {
				return err
			}
			head = added.ID
		}
		return setHead(ctx, q, sessionID, head)
	})
}

// SetSessionHead points a session's head at one of its own entries, which is
// how a branch starts: the next append hangs off that entry instead of the
// one that was last written. An empty entryID clears the head, so the session
// starts again from its root. Preamble entries cannot be branch points.
func (s *Store) SetSessionHead(ctx context.Context, sessionID, entryID string) error {
	// The entry check is part of the update, so that no head can be set from
	// a row that another connection deleted in between.
	const q = `UPDATE sessions SET head_entry_id = $2, updated_at = now()
		WHERE id = $1 AND ($2::text IS NULL OR EXISTS (
			SELECT 1 FROM session_entries WHERE id = $2 AND session_id = $1 AND kind <> 'preamble'))`
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

// EntriesBefore returns up to limit entries in write order before seq. A zero
// seq starts at the newest entry. Entries from all branches are included.
func (s *Store) EntriesBefore(ctx context.Context, sessionID string, seq int64, limit int) ([]Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM (SELECT ` + entryColumns + ` FROM session_entries
 WHERE session_id = $1 AND ($2::bigint = 0 OR seq < $2) ORDER BY seq DESC LIMIT $3) page ORDER BY seq`
	return s.queryEntries(ctx, "page entries of session "+sessionID, q, sessionID, seq, limit)
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
		SELECT e.id, e.session_id, e.parent_id, e.seq, e.kind, e.payload, e.created_at, e.channel, e.run_id
		FROM session_entries e JOIN up ON e.id = up.parent_id
	)
	SELECT ` + entryColumns + ` FROM up ORDER BY seq`

// insertEntry writes one entry under parent, giving it the next sequence
// number of its session, and e.ID, or a fresh id when that is empty.
//
// The caller must hold the session row lock that lockSessionHead takes:
// max(seq) + 1 is only unique while one writer at a time reads it. Two
// concurrent appends without the lock would pick the same number and one of
// them would lose the (session_id, seq) uniqueness check.
func insertEntry(ctx context.Context, q querier, sessionID, parentID string, e Entry) (Entry, error) {
	const insert = `INSERT INTO session_entries (id, session_id, parent_id, seq, kind, payload, channel, run_id)
		VALUES ($1, $2, $3,
			(SELECT coalesce(max(seq), 0) + 1 FROM session_entries WHERE session_id = $2),
			$4, $5, $6, $7)
		RETURNING ` + entryColumns
	payload := e.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	id := e.ID
	if id == "" {
		id = NewID()
	}
	row := q.QueryRow(ctx, insert, id, sessionID, nullable(parentID), e.Kind, []byte(payload), e.Channel, nullable(e.RunID))
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
		runID   *string
		payload []byte
	)
	err := row.Scan(&e.ID, &e.SessionID, &parent, &e.Seq, &e.Kind, &payload, &e.CreatedAt, &e.Channel, &runID)
	if err != nil {
		return Entry{}, err
	}
	e.ParentID = text(parent)
	e.RunID = text(runID)
	e.Payload = json.RawMessage(payload)
	e.CreatedAt = e.CreatedAt.UTC()
	return e, nil
}

// UndeliveredNotices returns a session's notice entries, on any branch, that
// no user entry of the session lists among the notices it delivered (the
// `notices` array of its message payload), oldest first.
func (s *Store) UndeliveredNotices(ctx context.Context, sessionID string) ([]Entry, error) {
	const q = `SELECT ` + entryColumns + ` FROM session_entries n
		WHERE n.session_id = $1 AND n.kind = 'notice' AND NOT EXISTS (
			SELECT 1 FROM session_entries u
			WHERE u.session_id = $1 AND u.kind = 'user' AND (u.payload::jsonb -> 'notices') ? n.id)
		ORDER BY n.seq`
	return s.queryEntries(ctx, "list undelivered notices of session "+sessionID, q, sessionID)
}
