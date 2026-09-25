package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/mcp/oauth"
)

// HeaderFunc returns the headers every request to a server carries: the ones
// the user configured and the access token. It is asked before each request,
// so a refreshed token applies at once.
type HeaderFunc func(ctx context.Context) (http.Header, error)

// httpTransport speaks Streamable HTTP to one endpoint. Every message is a
// POST, and a request's answer comes back as one JSON object or as an event
// stream scoped to it. A legacy connection also keeps the session id the
// server assigned and a GET stream for what the server sends unasked.
type httpTransport struct {
	url     string
	client  *http.Client
	headers HeaderFunc
	recv    receiver

	// ctx lives as long as the transport; every stream it opens ends with it.
	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu sync.Mutex
	// legacy is set once an initialize handshake succeeded: from then on the
	// session id is sent and cancellations are notifications.
	legacy    bool
	sessionID string
	version   string
	// closed is set once close began; no goroutine starts after it.
	closed bool
}

// newHTTPTransport returns a transport to the endpoint at url.
func newHTTPTransport(url string, client *http.Client, headers HeaderFunc, recv receiver) *httpTransport {
	ctx, stop := context.WithCancel(context.Background())
	return &httpTransport{url: url, client: client, headers: headers, recv: recv, ctx: ctx, stop: stop}
}

// becomeLegacy records the outcome of an initialize handshake.
func (t *httpTransport) becomeLegacy(version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.legacy, t.version = true, version
}

func (t *httpTransport) send(ctx context.Context, m *message, h sendHeaders) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode %s: %w", h.method, err)
	}
	// A request lives as long as its caller's context and the transport,
	// whichever ends first. Ending it closes its stream, which on a modern
	// server is the cancellation.
	reqCtx, cancel := context.WithCancel(ctx)
	stopAfter := context.AfterFunc(t.ctx, cancel)
	release := func() {
		stopAfter()
		cancel()
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		release()
		return fmt.Errorf("build request: %w", err)
	}
	if err := t.setHeaders(req, h); err != nil {
		release()
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := t.client.Do(req)
	if err != nil {
		release()
		return fmt.Errorf("post %s: %w", h.method, err)
	}
	t.keepSession(resp, h)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer release()
		defer resp.Body.Close()
		t.mu.Lock()
		inSession := t.legacy && t.sessionID != ""
		t.mu.Unlock()
		return statusError(resp, inSession)
	}
	key := ""
	if m.isRequest() {
		key = idKey(m.ID)
	}
	if key == "" || resp.StatusCode == http.StatusAccepted {
		// A notification or a response is acknowledged, not answered.
		drain(resp.Body)
		release()
		if key != "" {
			return fmt.Errorf("%s: the server accepted the request and sent no answer", h.method)
		}
		return nil
	}
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch contentType {
	case "application/json":
		defer release()
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes+1))
		if err != nil {
			return fmt.Errorf("read %s answer: %w", h.method, err)
		}
		if len(data) > maxMessageBytes {
			return fmt.Errorf("%s: the answer is larger than %d bytes", h.method, maxMessageBytes)
		}
		msgs, err := decodeMessages(data)
		if err != nil {
			return fmt.Errorf("%s: %w", h.method, err)
		}
		answered := false
		for _, msg := range msgs {
			answered = answered || (msg.isResponse() && idKey(msg.ID) == key)
			t.recv.receive(msg, key)
		}
		if !answered {
			return fmt.Errorf("%s: the answer held no response to the request", h.method)
		}
		return nil
	case "text/event-stream":
		if !t.spawn(func() { t.readStream(resp, key, release) }) {
			resp.Body.Close()
			release()
			return errTransportClosed
		}
		return nil
	default:
		release()
		resp.Body.Close()
		return fmt.Errorf("%s: the server answered with %q, not JSON or an event stream", h.method, contentType)
	}
}

// setHeaders writes the configured headers, the token, and the MCP headers
// onto a request.
func (t *httpTransport) setHeaders(req *http.Request, h sendHeaders) error {
	if t.headers != nil {
		extra, err := t.headers(req.Context())
		if err != nil {
			return err
		}
		for name, values := range extra {
			for _, v := range values {
				req.Header.Add(name, v)
			}
		}
	}
	if h.version != "" {
		req.Header.Set("MCP-Protocol-Version", h.version)
	}
	if h.modern {
		req.Header.Set("Mcp-Method", h.method)
		if h.name != "" {
			req.Header.Set("Mcp-Name", headerValue(h.name))
		}
		for name, value := range h.params {
			req.Header.Set("Mcp-Param-"+name, headerValue(value))
		}
	}
	t.mu.Lock()
	session := t.sessionID
	t.mu.Unlock()
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	return nil
}

// keepSession records the session id a legacy server assigns in its answer
// to initialize.
func (t *httpTransport) keepSession(resp *http.Response, h sendHeaders) {
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" && h.method == "initialize" {
		t.mu.Lock()
		t.sessionID = id
		t.mu.Unlock()
	}
}

// readStream delivers the messages of a request's event stream until its
// response arrives or the stream ends.
func (t *httpTransport) readStream(resp *http.Response, key string, release func()) {
	defer release()
	defer resp.Body.Close()
	events := newSSEReader(resp.Body)
	for {
		ev, err := events.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = errStreamBroken
			}
			t.recv.lost(key, err)
			return
		}
		if ev.name != "message" {
			continue
		}
		msgs, err := decodeMessages([]byte(ev.data))
		if err != nil {
			continue
		}
		for _, m := range msgs {
			t.recv.receive(m, key)
			if m.isResponse() && idKey(m.ID) == key {
				return
			}
		}
	}
}

// statusError turns an HTTP failure into the error the client acts on. A
// 404 inside a legacy session means the server forgot the session.
func statusError(resp *http.Response, inSession bool) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return &AuthError{Status: resp.StatusCode, Challenge: oauth.ParseChallenge(resp.Header.Values("WWW-Authenticate"))}
	case http.StatusForbidden:
		if c := oauth.ParseChallenge(resp.Header.Values("WWW-Authenticate")); c.Error == "insufficient_scope" {
			return &AuthError{Status: resp.StatusCode, Challenge: c}
		}
	case http.StatusNotFound:
		if inSession {
			return ErrSessionExpired
		}
	}
	e := &HTTPError{Status: resp.StatusCode}
	if msgs, err := decodeMessages(body); err == nil && len(msgs) == 1 && msgs[0].Error != nil {
		e.RPC = msgs[0].Error
	} else {
		e.Body = quote(string(body))
	}
	return e
}

func (t *httpTransport) cancelled(ctx context.Context, id string) {
	t.mu.Lock()
	legacy, version := t.legacy, t.version
	t.mu.Unlock()
	if !legacy {
		// Ending the request closed its stream, which is the cancellation.
		return
	}
	m, err := notification(notifyCancelled, map[string]any{"requestId": json.RawMessage(id), "reason": "the caller gave up"})
	if err != nil {
		return
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	_ = t.send(sendCtx, m, sendHeaders{version: version, method: notifyCancelled})
}

// listen keeps the GET stream a legacy server sends unasked messages on,
// opening it again when it drops, until the transport closes. A server that
// offers no such stream answers 405 and is left alone.
func (t *httpTransport) listen() {
	t.spawn(func() {
		backoff := time.Second
		for t.ctx.Err() == nil {
			opened, err := t.openListenStream()
			if err != nil || !opened {
				return
			}
			select {
			case <-t.ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, 30*time.Second)
		}
	})
}

// spawn runs fn on a goroutine close waits for, unless close began.
func (t *httpTransport) spawn(fn func()) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		fn()
	}()
	return true
}

// openListenStream reads one GET stream to its end. It reports false when
// the server does not offer one, which ends the listening for good.
func (t *httpTransport) openListenStream() (bool, error) {
	req, err := http.NewRequestWithContext(t.ctx, http.MethodGet, t.url, nil)
	if err != nil {
		return false, err
	}
	t.mu.Lock()
	version := t.version
	t.mu.Unlock()
	if err := t.setHeaders(req, sendHeaders{version: version}); err != nil {
		return false, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := t.client.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || contentType != "text/event-stream" {
		drain(resp.Body)
		return false, nil
	}
	events := newSSEReader(resp.Body)
	for {
		ev, err := events.next()
		if err != nil {
			return true, nil
		}
		if ev.name != "message" {
			continue
		}
		msgs, err := decodeMessages([]byte(ev.data))
		if err != nil {
			continue
		}
		for _, m := range msgs {
			t.recv.receive(m, "")
		}
	}
}

func (t *httpTransport) close() error {
	t.mu.Lock()
	t.closed = true
	session, version := t.sessionID, t.version
	t.mu.Unlock()
	t.stop()
	t.wg.Wait()
	if session == "" {
		return nil
	}
	// A legacy session is ended with a DELETE, which a server may refuse
	// with 405; either way the client is done with it.
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, t.url, nil)
	if err != nil {
		return nil
	}
	if err := t.setHeaders(req, sendHeaders{version: version}); err != nil {
		return nil
	}
	if resp, err := t.client.Do(req); err == nil {
		drain(resp.Body)
	}
	return nil
}

// drain reads what is left of a body, bounded, and closes it, so that the
// connection can be reused.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxErrorBody))
	_ = body.Close()
}
