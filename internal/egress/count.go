package egress

import (
	"io"
	"net/http"
	"sync"
)

// sizeStep is how many bytes a connection moves between two of its reports.
const sizeStep = 64 << 10

// Report is what the proxy knows of one allowed connection: its host and the
// bytes moved since the last report. The proxy sees no content.
type Report struct {
	// Computer is the computer that opened the connection.
	Computer string
	// Session and Run are what Options.Run named when it started.
	Session, Run string
	// Host is the host the computer asked for.
	Host string
	// Sent and Received are bytes since the last report: sent from the
	// computer to the host, received the other way.
	Sent, Received int64
	// Closed marks the connection's last report.
	Closed bool
}

// meter counts one connection and reports it. A nil meter counts nothing.
type meter struct {
	report func(Report)
	base   Report

	mu        sync.Mutex
	sent, got int64
	failed    bool
}

// meter returns the meter of a connection that was just allowed, or nil when
// nothing would use it.
func (p *Proxy) meter(computer, host string) *meter {
	if p.opts.Report == nil || p.opts.Run == nil {
		return nil
	}
	session, run := p.opts.Run(computer)
	if run == "" {
		return nil
	}
	return &meter{report: p.opts.Report, base: Report{Computer: computer, Session: session, Run: run, Host: host}}
}

func (m *meter) count(sent, got int64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.sent += sent
	m.got += got
	var out Report
	flush := !m.failed && m.sent+m.got >= sizeStep
	if flush {
		out = m.drain(false)
	}
	m.mu.Unlock()
	if flush {
		m.report(out)
	}
}

// drain takes the unreported counts. The caller holds mu.
func (m *meter) drain(closed bool) Report {
	out := m.base
	out.Sent, out.Received, out.Closed = m.sent, m.got, closed
	m.sent, m.got = 0, 0
	return out
}

// fail marks a connection that never reached its host, which reports nothing.
func (m *meter) fail() {
	m.mu.Lock()
	m.failed = true
	m.mu.Unlock()
}

// finish sends the last report.
func (m *meter) finish() {
	m.mu.Lock()
	if m.failed {
		m.mu.Unlock()
		return
	}
	out := m.drain(true)
	m.mu.Unlock()
	m.report(out)
}

// countedConn counts what a tunnel relays in one direction.
type countedConn struct {
	w    io.Writer
	m    *meter
	sent bool
}

func (c *countedConn) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	if c.sent {
		c.m.count(int64(n), 0)
	} else {
		c.m.count(0, int64(n))
	}
	return n, err
}

// countedBody counts a plain request's body as sent.
type countedBody struct {
	io.ReadCloser
	m *meter
}

func (b *countedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.m.count(int64(n), 0)
	return n, err
}

// countedWriter counts a plain request's response body as received. Unwrap
// lets flushing reach the real writer.
type countedWriter struct {
	http.ResponseWriter
	m *meter
}

func (w *countedWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.m.count(0, int64(n))
	return n, err
}

func (w *countedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
