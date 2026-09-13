package builtin

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNoQuestion reports that no run is waiting for an answer to the given
// question, because it was answered already or its run ended.
var ErrNoQuestion = errors.New("question not found")

// ErrBadAnswer reports an answer a question does not accept, such as free
// text for a question that only offers options.
var ErrBadAnswer = errors.New("invalid answer")

// Questions holds the questions runs are waiting on. The ask_user tool
// registers one and blocks; the server answers it from an HTTP request. One
// broker is shared by every run: a question id names a question anywhere.
type Questions struct {
	mu      sync.Mutex
	pending map[string]*question
}

// NewQuestions returns an empty broker.
func NewQuestions() *Questions {
	return &Questions{pending: make(map[string]*question)}
}

// Question is one unanswered question, as the API reports it.
type Question struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id"`
	RunID         string    `json:"run_id"`
	CallID        string    `json:"call_id"`
	Text          string    `json:"question"`
	Options       []string  `json:"options,omitempty"`
	AllowFreeText bool      `json:"allow_free_text"`
	AskedAt       time.Time `json:"asked_at"`
}

// question is a pending question and the channel its answer arrives on.
type question struct {
	Question
	answer chan string
}

// Answer delivers the answer to a waiting question. It reports ErrNoQuestion
// when nothing is waiting and ErrBadAnswer when the question does not accept
// this answer.
func (q *Questions) Answer(id, answer string) error {
	q.mu.Lock()
	pending, ok := q.pending[id]
	if ok {
		delete(q.pending, id)
	}
	q.mu.Unlock()
	if !ok {
		return fmt.Errorf("answer question %s: %w", id, ErrNoQuestion)
	}
	if err := pending.accepts(answer); err != nil {
		q.mu.Lock()
		q.pending[id] = pending
		q.mu.Unlock()
		return err
	}
	// The channel is buffered and the question is out of the map, so exactly
	// one answer is delivered and no answerer ever blocks.
	pending.answer <- answer
	return nil
}

// Pending returns the questions waiting for an answer, oldest first.
func (q *Questions) Pending() []Question {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Question, 0, len(q.pending))
	for _, p := range q.pending {
		out = append(out, p.Question)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].AskedAt.Before(out[j-1].AskedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ask registers a question and returns it with the channel its answer
// arrives on.
func (q *Questions) ask(asked Question) *question {
	pending := &question{Question: asked, answer: make(chan string, 1)}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending[asked.ID] = pending
	return pending
}

// forget drops a question nobody will answer any more, which is what an
// aborted run leaves behind.
func (q *Questions) forget(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.pending, id)
}

// accepts reports whether the question takes this answer.
func (q *question) accepts(answer string) error {
	if answer == "" {
		return fmt.Errorf("answer question %s: %w: the answer is empty", q.ID, ErrBadAnswer)
	}
	if len(q.Options) == 0 || q.AllowFreeText {
		return nil
	}
	for _, o := range q.Options {
		if o == answer {
			return nil
		}
	}
	return fmt.Errorf("answer question %s: %w: %q is not one of the options", q.ID, ErrBadAnswer, answer)
}

// newQuestionID returns an identifier for one question.
func newQuestionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("q-%d", time.Now().UnixNano())
	}
	return "q-" + hex.EncodeToString(b[:])
}
