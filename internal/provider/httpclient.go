package provider

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"syscall"
	"time"
)

// NewHTTPClient returns the HTTP client a provider, its token counter and the
// probe use to reach an endpoint. It shares http.DefaultTransport's pool and
// differs in one thing: a request that fails because the server had already
// closed the idle connection it was written to is sent again on another
// connection.
//
// Go does that itself only for requests that are safe to repeat, and every
// request to a model is a POST. Some servers close a connection when a
// streamed response ends (llama.cpp does), and the client may pick that
// connection from its pool before it has seen the close, particularly behind
// an SSH forward, which delays the close it relays. The request then fails
// with EOF before a byte of a response arrives. The server answered nothing,
// so a repeat is what the caller wants. A failure on a connection that was
// new is never repeated. A timeout of zero means none, which a streamed
// response needs.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: staleConnRetry{}}
}

// staleConnRetry is the transport NewHTTPClient installs.
type staleConnRetry struct{}

// maxStaleAttempts bounds how many idle connections one request may find
// closed. A failure on a connection that was new ends the request at once, so
// this only matters for a pool holding several connections the server closed.
const maxStaleAttempts = 4

// RoundTrip sends req on the default transport and, each time the connection
// it used was a reused one that turned out to be closed, sends it again.
func (staleConnRetry) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		var reused bool
		trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.WasIdle }}
		resp, err := http.DefaultTransport.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
		if err == nil || !reused || !closedByServer(err) || req.Context().Err() != nil || attempt == maxStaleAttempts {
			return resp, err
		}
		again := req.Clone(req.Context())
		if req.Body != nil && req.Body != http.NoBody {
			if req.GetBody == nil {
				return resp, err
			}
			body, bodyErr := req.GetBody()
			if bodyErr != nil {
				return resp, err
			}
			again.Body = body
		}
		req = again
	}
}

// closedByServer reports whether err is what a write to a connection the
// server has closed looks like once the request is sent: the read finds the
// end of the stream, the connection reset, the transport has already closed
// the connection it saw the server end and the write lost the race to it, or
// the transport saw the end while the request was being written and reports
// that it closed an idle connection.
func closedByServer(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, net.ErrClosed) || isServerClosedIdle(err)
}

// serverClosedIdleMessage is the text of net/http's errServerClosedIdle.
const serverClosedIdleMessage = "http: server closed idle connection"

// isServerClosedIdle reports whether err is the error net/http returns for a
// request whose reused connection the server closed while the request was
// being written. net/http keeps that value unexported and wraps nothing around
// it, so no type or sentinel can be tested for, and the message is the only
// thing left. Matching the whole message of an error taken as it stands, never
// a substring of a wrapped one, is the narrowest test; a Go release that
// rewords it fails the regression test that provokes the real error.
func isServerClosedIdle(err error) bool {
	return err != nil && err.Error() == serverClosedIdleMessage
}
