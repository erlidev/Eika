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

// Truncate drops every message after the first n. It is how an aborted turn
// takes back the messages the model never answered.
func (c *Conversation) Truncate(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < 0 {
		n = 0
	}
	if n < len(c.messages) {
		c.messages = c.messages[:n]
	}
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

// Store persists the messages a run produces. session.Store implements it
// against the database.
type Store interface {
	Append(ctx context.Context, sessionID string, m provider.Message) error
}
