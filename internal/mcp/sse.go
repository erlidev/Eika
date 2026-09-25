package mcp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

// sseEvent is one server-sent event.
type sseEvent struct {
	// name is the event field, "message" when the server sent none.
	name string
	data string
}

// sseReader reads the events of a text/event-stream body, as the HTML
// living standard defines them: fields up to a blank line, comments
// ignored, and any of CRLF, LF, or CR ending a line. Event ids are not
// kept: the client does not resume a broken stream, which the 2026-07-28
// revision removed and earlier ones left optional.
type sseReader struct {
	scan *bufio.Scanner
}

// newSSEReader reads events from r, none of them larger than maxMessageBytes.
func newSSEReader(r io.Reader) *sseReader {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	scan.Split(scanSSELines)
	return &sseReader{scan: scan}
}

// errEventTooLarge reports an event past maxMessageBytes.
var errEventTooLarge = errors.New("server-sent event too large")

// next returns the next event with data. It returns io.EOF when the stream
// ends between events.
func (s *sseReader) next() (sseEvent, error) {
	var (
		ev      sseEvent
		data    strings.Builder
		hasData bool
	)
	for s.scan.Scan() {
		line := s.scan.Text()
		if line == "" {
			if !hasData {
				// An event with no data is not dispatched.
				ev.name = ""
				continue
			}
			ev.data = strings.TrimSuffix(data.String(), "\n")
			if ev.name == "" {
				ev.name = "message"
			}
			return ev, nil
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev.name = value
		case "data":
			if data.Len()+len(value) > maxMessageBytes {
				return sseEvent{}, errEventTooLarge
			}
			data.WriteString(value)
			data.WriteByte('\n')
			hasData = true
		}
	}
	if err := s.scan.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return sseEvent{}, errEventTooLarge
		}
		return sseEvent{}, fmt.Errorf("read event stream: %w", err)
	}
	return sseEvent{}, io.EOF
}

// scanSSELines splits a stream into lines ended by CRLF, LF, or CR.
func scanSSELines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		if data[i] == '\n' {
			return i + 1, data[:i], nil
		}
		// A CR may be the first half of a CRLF still on its way.
		if i+1 < len(data) {
			if data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
			return i + 1, data[:i], nil
		}
		if atEOF {
			return i + 1, data[:i], nil
		}
		return 0, nil, nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
