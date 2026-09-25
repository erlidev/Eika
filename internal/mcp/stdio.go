package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// stdioTransport speaks newline-delimited JSON-RPC over a process's standard
// streams: it writes to the process's stdin and reads its stdout. Where the
// process runs is the Launcher's business; for Eika it is a workspace.
type stdioTransport struct {
	rw   io.ReadWriteCloser
	recv receiver

	// mu keeps one message from interleaving with another on stdin.
	mu sync.Mutex
	wg sync.WaitGroup
}

// newStdioTransport returns a transport over rw; start begins reading.
func newStdioTransport(rw io.ReadWriteCloser, recv receiver) *stdioTransport {
	return &stdioTransport{rw: rw, recv: recv}
}

// start reads the process's stdout until it ends.
func (t *stdioTransport) start() {
	t.wg.Add(1)
	go t.read()
}

// read delivers every line of stdout as a message until the stream ends.
func (t *stdioTransport) read() {
	defer t.wg.Done()
	scan := bufio.NewScanner(t.rw)
	scan.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	for scan.Scan() {
		line := bytes.TrimSpace(scan.Bytes())
		if len(line) == 0 {
			continue
		}
		msgs, err := decodeMessages(line)
		if err != nil {
			// A server must write nothing but messages to stdout, but a stray
			// line from a library it uses should not end the connection.
			continue
		}
		for _, m := range msgs {
			t.recv.receive(m, "")
		}
	}
	err := scan.Err()
	switch {
	case errors.Is(err, bufio.ErrTooLong):
		err = fmt.Errorf("the server wrote a message larger than %d bytes", maxMessageBytes)
	case err == nil:
		err = errors.New("the server process closed its output")
	}
	t.recv.closed(err)
}

func (t *stdioTransport) send(_ context.Context, m *message, h sendHeaders) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode %s: %w", h.method, err)
	}
	// encoding/json escapes every newline inside a string, so the only one in
	// the line is the one that ends it.
	data = append(data, '\n')
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.rw.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", h.method, err)
	}
	return nil
}

func (t *stdioTransport) cancelled(ctx context.Context, id string) {
	m, err := notification(notifyCancelled, map[string]any{"requestId": json.RawMessage(id), "reason": "the caller gave up"})
	if err != nil {
		return
	}
	_ = t.send(ctx, m, sendHeaders{method: notifyCancelled})
}

// close closes the streams, which a well-behaved server takes as the signal
// to exit; the Launcher's process ends it otherwise.
func (t *stdioTransport) close() error {
	err := t.rw.Close()
	t.wg.Wait()
	return err
}
