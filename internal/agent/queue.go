package agent

import (
	"sync"

	"github.com/erlidev/eika/internal/provider"
)

// queue is a first-in, first-out list of user messages waiting to join the
// conversation. Steering messages and follow-up messages each have one.
type queue struct {
	mu       sync.Mutex
	messages []provider.Message
}

// push adds a message to the back.
func (q *queue) push(msg provider.Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, msg)
}

// unshift puts messages back at the front, in their original order. It is what
// an aborted run does with the messages it had taken but never delivered.
func (q *queue) unshift(msgs []provider.Message) {
	if len(msgs) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(append([]provider.Message(nil), msgs...), q.messages...)
}

// drain takes every queued message.
func (q *queue) drain() []provider.Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.messages
	q.messages = nil
	return out
}

// shift removes the message at the front, returning it alone, or nothing
// when the queue is empty.
func (q *queue) shift() []provider.Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return nil
	}
	out := q.messages[:1:1]
	q.messages = q.messages[1:]
	return out
}

// pending returns the queued messages without removing them.
func (q *queue) pending() []provider.Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]provider.Message(nil), q.messages...)
}
