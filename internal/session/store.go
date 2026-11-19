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
	tree    *Tree
	channel string
}

// NewStore returns a store that appends to tree.
func NewStore(tree *Tree) *Store {
	return &Store{tree: tree}
}

// SetChannel records the channel of user and assistant messages.
func (s *Store) SetChannel(channel string) { s.channel = channel }

// Append records one message of a run.
func (s *Store) Append(ctx context.Context, sessionID, runID string, m provider.Message) error {
	e, err := MessageEntry(m)
	if err != nil {
		return err
	}
	e.RunID = runID
	if m.Role == provider.RoleUser || m.Role == provider.RoleAssistant {
		e.Channel = s.channel
	}
	if _, err := s.tree.Append(ctx, sessionID, e); err != nil {
		return err
	}
	return nil
}

// AppendCompaction records a compaction of a run.
func (s *Store) AppendCompaction(ctx context.Context, sessionID, runID string, c agent.Compaction) error {
	e, err := CompactionEntry(c)
	if err != nil {
		return err
	}
	e.RunID = runID
	_, err = s.tree.Append(ctx, sessionID, e)
	return err
}

// Load rebuilds the session a run works on from the database: the computer
// it belongs to and the conversation on its current branch. A run started
// from a loaded session continues where the last one stopped, and a run
// started after SetHead continues the branch the head now points at.
func (s *Store) Load(ctx context.Context, sessionID string) (*agent.Session, error) {
	sess, err := s.tree.store.Session(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	path, err := s.tree.Path(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages, err := Messages(path)
	if err != nil {
		return nil, err
	}
	snap, err := SnapshotOf(path)
	if err != nil {
		return nil, err
	}
	return &agent.Session{
		ID:           sess.ID,
		Preamble:     snap.Text,
		Prompt:       snap.Prompt,
		Tools:        snap.Tools,
		ComputerID:   sess.ComputerID,
		Conversation: agent.NewConversation(messages...),
	}, nil
}

var _ agent.Store = (*Store)(nil)
