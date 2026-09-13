package session

import (
	"context"
	"fmt"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
)

// CommitFunc reports the workspace HEAD commit a session's workspace is at.
// A Store calls it once per assistant message, so that the entry records the
// state of the files the model produced its answer from, which is what a fork
// with a workspace clones at.
type CommitFunc func(ctx context.Context, sessionID string) (string, error)

// Store records the messages of a run in the database, one entry per message,
// appended at the session's head. It is the production implementation of
// agent.Store.
type Store struct {
	tree   *Tree
	commit CommitFunc
}

// NewStore returns a store that appends to tree. A nil commit records no
// commits, which is what a session without a workspace of its own wants.
func NewStore(tree *Tree, commit CommitFunc) *Store {
	return &Store{tree: tree, commit: commit}
}

// Append records one message of a run.
func (s *Store) Append(ctx context.Context, sessionID string, m provider.Message) error {
	commit := ""
	if m.Role == provider.RoleAssistant && s.commit != nil {
		var err error
		if commit, err = s.commit(ctx, sessionID); err != nil {
			return fmt.Errorf("read workspace commit for session %s: %w", sessionID, err)
		}
	}
	if _, err := s.tree.AppendMessage(ctx, sessionID, m, commit); err != nil {
		return err
	}
	return nil
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
