package builtin

import (
	"fmt"
	"strings"
	"time"
)

// Every bound the built-in tools apply lives here, so that one file answers
// "how much can a tool return".
const (
	// maxOutputBytes is the most any command-running tool returns.
	maxOutputBytes = 32 * 1024
	// outputHeadBytes is how much of the start of a long output survives
	// truncation; the rest of the budget keeps the end.
	outputHeadBytes = 20 * 1024
	// outputTailBytes is how much of the end of a long output survives.
	outputTailBytes = maxOutputBytes - outputHeadBytes

	// defaultBashTimeout bounds a command that does not ask for a timeout.
	defaultBashTimeout = 2 * time.Minute
	// maxBashTimeout bounds a command that asks for too much.
	maxBashTimeout = 10 * time.Minute
)

// output captures a command's combined output, keeping the start and the end
// when it grows past the budget. It is an io.Writer so that it can be handed
// to the executor directly.
type output struct {
	head  []byte
	tail  []byte
	total int
	// streamed counts the bytes already sent to stream, so that a command
	// that never stops printing cannot flood the event stream.
	streamed int
	// stream receives chunks as they arrive, for the tool.output events.
	stream func(string)
}

// Write records a chunk and forwards it to the stream callback.
func (o *output) Write(p []byte) (int, error) {
	n := len(p)
	o.total += n
	o.forward(p)
	if room := outputHeadBytes - len(o.head); room > 0 {
		take := min(room, len(p))
		o.head = append(o.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		o.tail = append(o.tail, p...)
		if len(o.tail) > outputTailBytes {
			o.tail = o.tail[len(o.tail)-outputTailBytes:]
		}
	}
	return n, nil
}

// forward sends a chunk to the stream callback until the stream budget is
// spent, then says once that the rest is not being streamed.
func (o *output) forward(p []byte) {
	if o.stream == nil || o.streamed > maxOutputBytes {
		return
	}
	room := maxOutputBytes - o.streamed
	if len(p) <= room {
		o.streamed += len(p)
		o.stream(string(p))
		return
	}
	o.streamed = maxOutputBytes + 1
	if room > 0 {
		o.stream(string(p[:room]))
	}
	o.stream("\n... output continues; the rest arrives with the result.\n")
}

// String returns the captured output, with a note in place of the part that
// was dropped.
func (o *output) String() string {
	dropped := o.total - len(o.head) - len(o.tail)
	if len(o.tail) == 0 || dropped <= 0 {
		return string(o.head) + string(o.tail)
	}
	var b strings.Builder
	b.Write(o.head)
	fmt.Fprintf(&b, "\n\n... [%d bytes truncated, showing the first %d and the last %d] ...\n\n", dropped, len(o.head), len(o.tail))
	b.Write(o.tail)
	return b.String()
}
