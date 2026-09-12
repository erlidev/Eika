package agent

import "sync"

// QueueMode decides how many queued follow-up messages one turn delivers.
type QueueMode string

// The queue modes, following Pi.
const (
	// QueueOneAtATime starts a new turn with a single queued message and
	// leaves the rest for the turns after it. It is the default.
	QueueOneAtATime QueueMode = "one-at-a-time"
	// QueueAll delivers every queued message at the start of the next turn.
	QueueAll QueueMode = "all"
)

// queue is a first-in, first-out list of user messages waiting to join the
// conversation. Steering messages and follow-up messages each have one.
type queue struct {
	mu       sync.Mutex
	messages []string
}

// push adds a message to the back.
func (q *queue) push(msg string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, msg)
}

// unshift puts messages back at the front, in their original order. It is what
// an aborted run does with the messages it had taken but never delivered.
func (q *queue) unshift(msgs []string) {
	if len(msgs) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(append([]string(nil), msgs...), q.messages...)
}

// drain takes every queued message.
func (q *queue) drain() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.messages
	q.messages = nil
	return out
}

// take removes up to n messages from the front. A non-positive n takes all.
func (q *queue) take(n int) []string {
	if n <= 0 {
		return q.drain()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if n > len(q.messages) {
		n = len(q.messages)
	}
	out := append([]string(nil), q.messages[:n]...)
	q.messages = q.messages[n:]
	return out
}

// pending returns the queued messages without removing them.
func (q *queue) pending() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.messages...)
}
