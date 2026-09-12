package builtin

import (
	"fmt"
	"strings"
	"time"
)

// Every bound the built-in tools apply lives here, so that one file answers
// "how much can a tool return".
const (
	// maxFileBytes is the most a single read returns.
	maxFileBytes = 256 * 1024
	// maxLineRunes is the longest line a read returns before eliding the rest.
	maxLineRunes = 2000
	// defaultReadLines is how many lines a read returns without a limit.
	defaultReadLines = 2000
	// binarySniffBytes is how much of a file is checked for NUL bytes before
	// the read refuses it as binary.
	binarySniffBytes = 8 * 1024

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

	// defaultMatchLimit is how many grep matches a call returns by default.
	defaultMatchLimit = 100
	// defaultFindLimit is how many paths a find returns by default.
	defaultFindLimit = 200
	// defaultListLimit is how many entries an ls returns by default.
	defaultListLimit = 500
)

// output captures a command's combined output, keeping the start and the end
// when it grows past the budget. It is an io.Writer so that it can be handed
// to the executor directly.
type output struct {
	head  []byte
	tail  []byte
	total int
	// stream receives every chunk as it arrives, for the tool.output events.
	stream func(string)
}

// Write records a chunk and forwards it to the stream callback.
func (o *output) Write(p []byte) (int, error) {
	n := len(p)
	o.total += n
	if o.stream != nil {
		o.stream(string(p))
	}
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

// String returns the captured output, with a note in place of the part that
// was dropped.
func (o *output) String() string {
	if len(o.tail) == 0 {
		return string(o.head)
	}
	dropped := o.total - len(o.head) - len(o.tail)
	var b strings.Builder
	b.Write(o.head)
	fmt.Fprintf(&b, "\n\n... [%d bytes truncated, showing the first %d and the last %d] ...\n\n", dropped, len(o.head), len(o.tail))
	b.Write(o.tail)
	return b.String()
}
