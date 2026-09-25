package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/erlidev/eika/internal/mcp/oauth"
)

// The transports a connection can run over.
const (
	// TransportStreamableHTTP is the HTTP transport of every revision since
	// 2025-03-26.
	TransportStreamableHTTP = "streamable_http"
	// TransportSSE is the HTTP+SSE transport of 2024-11-05, deprecated and
	// used only when a server offers nothing else.
	TransportSSE = "sse"
	// TransportStdio is a process's standard streams.
	TransportStdio = "stdio"
)

// transport carries messages to one server and brings back what it sends.
// What the server sends reaches the receive callback the transport was built
// with, whichever way it arrives.
type transport interface {
	// send delivers one request, notification, or response. For a request,
	// a nil error means its response will reach receive, or that lost will
	// be called for it if the way it would arrive breaks first.
	send(ctx context.Context, m *message, h sendHeaders) error
	// cancelled tells the server that the client gave up on a request, in
	// whatever way the transport and the era say: a notification, or
	// nothing where closing the request's stream already said it.
	cancelled(ctx context.Context, id string)
	// close ends the connection and every stream it holds open.
	close() error
}

// sendHeaders is what the Streamable HTTP transport mirrors into headers.
// The other transports ignore it.
type sendHeaders struct {
	// version is the protocol version the message is sent under, which a
	// legacy initialize leaves empty.
	version string
	// modern marks a 2026-07-28 request, which carries Mcp-Method,
	// Mcp-Name, and Mcp-Param-* as well.
	modern bool
	method string
	// name is the tool or prompt name or the resource URI of a request that
	// has one.
	name string
	// params are the Mcp-Param-* headers of a tool call, by name.
	params map[string]string
}

// receiver is how a transport hands over what the server sent. via is the
// key of the request whose response stream carried the message, empty when
// it came on a channel every request shares.
type receiver struct {
	receive func(m *message, via string)
	// lost reports that a request's response will never arrive.
	lost func(id string, err error)
	// closed reports that a shared channel ended, taking every request
	// still waiting on it.
	closed func(err error)
}

// errStreamBroken reports a response stream that ended before its response.
var errStreamBroken = errors.New("the server's response stream ended before the response")

// errTransportClosed reports a request made as the connection closed.
var errTransportClosed = errors.New("the connection is closed")

// ErrSessionExpired reports that a legacy server no longer knows the
// connection's session, so the connection has to start again.
var ErrSessionExpired = errors.New("the server ended the session")

// AuthError reports that a server refused a request for want of
// authorization: 401 for a missing or invalid token, 403 with
// insufficient_scope for a token that lacks a scope.
type AuthError struct {
	Status    int
	Challenge oauth.Challenge
}

// Error says what the server wants.
func (e *AuthError) Error() string {
	if e.Status == http.StatusForbidden {
		if e.Challenge.Scope != "" {
			return fmt.Sprintf("the server needs more access (scope %q)", e.Challenge.Scope)
		}
		return "the server needs more access"
	}
	return "the server needs authorization"
}

// HTTPError reports an HTTP status the client did not expect, with the
// JSON-RPC error its body held, if any.
type HTTPError struct {
	Status int
	// RPC is the JSON-RPC error in the body, nil when there was none.
	RPC *RPCError
	// Body is the start of a body that was not a JSON-RPC error.
	Body string
}

// Error names the status and what the server said.
func (e *HTTPError) Error() string {
	text := fmt.Sprintf("HTTP %d %s", e.Status, http.StatusText(e.Status))
	switch {
	case e.RPC != nil:
		return text + ": " + e.RPC.Error()
	case e.Body != "":
		return text + ": " + e.Body
	}
	return text
}

// quote cuts a server's text to what a message should repeat.
func quote(s string) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxErrorQuoted {
		cut := maxErrorQuoted
		for cut > 0 && !isRuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
