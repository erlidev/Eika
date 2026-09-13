package session

import (
	"context"
	"fmt"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
)

// Tree is the session tree of one database: entries, heads, branches, and
// forks.
type Tree struct {
	store *store.Store
}

// NewTree returns a tree backed by s.
func NewTree(s *store.Store) *Tree { return &Tree{store: s} }

// Append writes e under the session's current head and makes it the new head.
// The caller supplies Kind, Payload, and Commit; the tree assigns the id, the
// parent, and the sequence number.
func (t *Tree) Append(ctx context.Context, sessionID string, e store.Entry) (store.Entry, error) {
	return t.store.AppendEntry(ctx, sessionID, e)
}

// AppendMessage records one provider message as an entry, tagged with the
// workspace HEAD commit it was produced at.
func (t *Tree) AppendMessage(ctx context.Context, sessionID string, m provider.Message, commit string) (store.Entry, error) {
	e, err := MessageEntry(m, commit)
	if err != nil {
		return store.Entry{}, err
	}
	return t.Append(ctx, sessionID, e)
}

// Head returns the entry a run continues from. A session with no entries has
// no head, which is store.ErrNotFound.
func (t *Tree) Head(ctx context.Context, sessionID string) (store.Entry, error) {
	sess, err := t.store.Session(ctx, sessionID)
	if err != nil {
		return store.Entry{}, err
	}
	if sess.HeadEntryID == "" {
		return store.Entry{}, fmt.Errorf("read head of session %s: %w", sessionID, store.ErrNotFound)
	}
	return t.store.Entry(ctx, sess.HeadEntryID)
}

// Path returns the entries from the session's root down to its head, oldest
// first. It is the branch the next run continues, and the conversation the
// model sees.
func (t *Tree) Path(ctx context.Context, sessionID string) ([]store.Entry, error) {
	return t.store.SessionPath(ctx, sessionID)
}

// Messages returns the session's path as provider messages.
func (t *Tree) Messages(ctx context.Context, sessionID string) ([]provider.Message, error) {
	path, err := t.Path(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return Messages(path)
}

// SetHead moves the head to one of the session's own entries. Appending after
// that branches the tree in place: the entries that used to follow stay where
// they are, on a branch of their own.
func (t *Tree) SetHead(ctx context.Context, sessionID, entryID string) error {
	return t.store.SetSessionHead(ctx, sessionID, entryID)
}

// Fork copies the path from the session's root down to entryID into a new
// session whose head is the copy of that entry. The fork shares no rows with
// its parent, so both continue independently; opts.WorkspaceID points the
// fork at a workspace cloned at the entry's commit.
func (t *Tree) Fork(ctx context.Context, sessionID, entryID string, opts store.ForkOptions) (store.Session, error) {
	return t.store.ForkSession(ctx, sessionID, entryID, opts)
}

// Children returns the entries that hang off entryID, oldest first. An empty
// entryID returns the session's roots. More than one child means the entry is
// a branch point.
func (t *Tree) Children(ctx context.Context, sessionID, entryID string) ([]store.Entry, error) {
	return t.store.ChildEntries(ctx, sessionID, entryID)
}
