package session

import (
	"context"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
)

// Store records the messages of a run in the database, one entry per message,
// appended at the session's head. It is the production implementation of
// agent.Store.
type Store struct {
	tree *Tree
}

// NewStore returns a store that appends to tree.
func NewStore(tree *Tree) *Store {
	return &Store{tree: tree}
}

// Append records one message of a run.
func (s *Store) Append(ctx context.Context, sessionID string, m provider.Message) error {
	if _, err := s.tree.AppendMessage(ctx, sessionID, m); err != nil {
		return err
	}
	return nil
}

// AppendCompaction records a compaction of a run.
func (s *Store) AppendCompaction(ctx context.Context, sessionID string, c agent.Compaction) error {
	e, err := CompactionEntry(c)
	if err != nil {
		return err
	}
	_, err = s.tree.Append(ctx, sessionID, e)
	return err
}

// Load rebuilds the session a run works on from the database: the workspace
// it belongs to and the conversation on its current branch. A run started
// from a loaded session continues where the last one stopped, and a run
// started after SetHead continues the branch the head now points at.
func (s *Store) Load(ctx context.Context, sessionID string) (*agent.Session, error) {
	sess, err := s.tree.store.Session(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages, err := s.tree.Messages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return &agent.Session{
		ID:           sess.ID,
		WorkspaceID:  sess.WorkspaceID,
		Conversation: agent.NewConversation(messages...),
	}, nil
}

// Tree returns the tree the store appends to, so that a caller that has one
// has the other.
func (s *Store) Tree() *Tree { return s.tree }

var _ agent.Store = (*Store)(nil)
