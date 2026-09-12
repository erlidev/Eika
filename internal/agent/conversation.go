package agent

import (
	"context"
	"sync"

	"github.com/erlidev/eika/internal/provider"
)

// Conversation is the ordered list of messages one session has exchanged. It
// is safe for concurrent use, because the user interface may read it while a
// run appends to it.
type Conversation struct {
	mu       sync.RWMutex
	messages []provider.Message
}

// NewConversation returns a conversation seeded with messages, which may be
// nil.
func NewConversation(messages ...provider.Message) *Conversation {
	return &Conversation{messages: append([]provider.Message(nil), messages...)}
}

// Append adds a message to the end of the conversation.
func (c *Conversation) Append(m provider.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, m)
}

// Messages returns a copy of the conversation.
func (c *Conversation) Messages() []provider.Message {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]provider.Message(nil), c.messages...)
}

// Len reports how many messages the conversation holds.
func (c *Conversation) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.messages)
}

// Session is the conversation an agent run operates on, inside one workspace.
type Session struct {
	ID           string
	WorkspaceID  string
	Conversation *Conversation
}

// NewSession returns an empty session.
func NewSession(id, workspaceID string) *Session {
	return &Session{ID: id, WorkspaceID: workspaceID, Conversation: NewConversation()}
}

// Store persists the messages a run produces. Phase 3 implements it against
// PostgreSQL; MemoryStore is the implementation the tests and single-process
// runs use.
type Store interface {
	Append(ctx context.Context, sessionID string, m provider.Message) error
}

// MemoryStore keeps messages in memory, keyed by session.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string][]provider.Message
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: map[string][]provider.Message{}}
}

// Append records one message.
func (s *MemoryStore) Append(_ context.Context, sessionID string, m provider.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string][]provider.Message{}
	}
	s.sessions[sessionID] = append(s.sessions[sessionID], m)
	return nil
}

// Messages returns the messages recorded for a session.
func (s *MemoryStore) Messages(sessionID string) []provider.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.Message(nil), s.sessions[sessionID]...)
}

var _ Store = (*MemoryStore)(nil)
